package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
)

// health is how a row should be coloured.
type health int

const (
	healthy  health = iota // default colour: nothing to see
	pending                // not there yet: starting, terminating, not all ready
	failing                // needs attention: crash loops, errors, NotReady
	finished               // done and gone quiet: Completed, Succeeded
)

var healthStyles = map[health]lipgloss.Style{
	healthy:  lipgloss.NewStyle(),
	pending:  lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B58900", Dark: "#F5C542"}),
	failing:  lipgloss.NewStyle().Foreground(colorError),
	finished: lipgloss.NewStyle().Foreground(colorMuted),
}

// Order matters: failures are checked first ("NotReady" contains "Ready",
// "Init:Error" starts with "Init:").
var (
	failingWords = []string{
		"Err", "BackOff", "Failed", "OOMKilled", "Evicted", "NotReady",
		"Unknown", "ExitCode:", "Signal:", "CannotRun", "Invalid", "DeadlineExceeded",
	}
	pendingWords = []string{
		"Pending", "ContainerCreating", "PodInitializing", "Terminating",
		"Init:", "SchedulingDisabled",
	}
	finishedWords = []string{"Completed", "Succeeded"}
)

// statusHealth classifies a STATUS value such as "CrashLoopBackOff".
func statusHealth(status string) health {
	switch {
	case containsAny(status, failingWords):
		return failing
	case containsAny(status, pendingWords):
		return pending
	case containsAny(status, finishedWords):
		return finished
	}
	return healthy
}

// rowHealth decides the colour of a table row. It only looks at data the
// row already has, so colouring never needs another API call.
func rowHealth(rt *k8s.ResourceType, r k8s.Resource) health {
	switch rt {
	case k8s.Pods:
		if r.Pod == nil {
			return healthy
		}
		h := statusHealth(r.Pod.Status)
		if h == healthy && r.Pod.Status == "Running" && !allReady(r.Pod.Ready) {
			return pending // running but failing its readiness probe
		}
		return h
	case k8s.Deployments:
		if !allReady(cell(r, 1)) {
			return pending
		}
	case k8s.Nodes:
		return statusHealth(cell(r, 1))
	case k8s.Events:
		if cell(r, 0) == "Warning" {
			return pending
		}
	}
	return healthy
}

// allReady reports whether a READY value "a/b" has a == b.
func allReady(ready string) bool {
	a, b, ok := strings.Cut(ready, "/")
	if !ok {
		return true
	}
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	return errA != nil || errB != nil || na >= nb
}

func cell(r k8s.Resource, i int) string {
	if i < len(r.Cells) {
		return r.Cells[i]
	}
	return ""
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
