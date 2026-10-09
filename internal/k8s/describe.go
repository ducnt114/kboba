package k8s

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/duration"
)

// DescribePod returns a kubectl-describe-like text for a pod, including its
// recent events. Failing to list events (e.g. RBAC) is not fatal: it is
// mentioned in the output instead.
func (c *client) DescribePod(ctx context.Context, namespace, name string) (string, error) {
	pod, err := c.GetPod(ctx, namespace, name)
	if err != nil {
		return "", err
	}

	selector := fields.Set{
		"involvedObject.kind":      "Pod",
		"involvedObject.name":      name,
		"involvedObject.namespace": namespace,
	}.AsSelector().String()
	list, evErr := c.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector})

	var events []corev1.Event
	if evErr == nil {
		for _, ev := range list.Items {
			// Double-check: not every API server (or fake) honours the selector.
			if ev.InvolvedObject.Kind == "Pod" && ev.InvolvedObject.Name == name {
				events = append(events, ev)
			}
		}
	}
	return formatPodDescription(pod, events, evErr, time.Now()), nil
}

func formatPodDescription(pod *corev1.Pod, events []corev1.Event, eventsErr error, now time.Time) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 8, 2, ' ', 0)
	age := func(t time.Time) string {
		if t.IsZero() {
			return "<unknown>"
		}
		return duration.HumanDuration(now.Sub(t))
	}

	field(w, "Name", pod.Name)
	field(w, "Namespace", pod.Namespace)
	field(w, "Node", orNone(pod.Spec.NodeName))
	if pod.Status.StartTime != nil {
		field(w, "Start Time", fmt.Sprintf("%s (%s ago)", pod.Status.StartTime.Format(time.RFC1123Z), age(pod.Status.StartTime.Time)))
	}
	field(w, "Labels", formatMap(pod.Labels))
	field(w, "Status", podStatus(pod))
	field(w, "Phase", string(pod.Status.Phase))
	field(w, "IP", orNone(pod.Status.PodIP))
	field(w, "QoS Class", orNone(string(pod.Status.QOSClass)))
	if len(pod.OwnerReferences) > 0 {
		o := pod.OwnerReferences[0]
		field(w, "Controlled By", o.Kind+"/"+o.Name)
	}
	if pod.Spec.ServiceAccountName != "" {
		field(w, "Service Account", pod.Spec.ServiceAccountName)
	}

	if len(pod.Spec.InitContainers) > 0 {
		fmt.Fprintln(w, "Init Containers:")
		writeContainers(w, pod.Spec.InitContainers, pod.Status.InitContainerStatuses, age)
	}
	fmt.Fprintln(w, "Containers:")
	writeContainers(w, pod.Spec.Containers, pod.Status.ContainerStatuses, age)

	fmt.Fprintln(w, "Conditions:")
	if len(pod.Status.Conditions) == 0 {
		fmt.Fprintln(w, "  <none>")
	} else {
		fmt.Fprintln(w, "  Type\tStatus\tReason")
		for _, c := range pod.Status.Conditions {
			fmt.Fprintf(w, "  %s\t%s\t%s\n", c.Type, c.Status, c.Reason)
		}
	}

	fmt.Fprintln(w, "Events:")
	switch {
	case eventsErr != nil:
		fmt.Fprintf(w, "  <unable to list events: %v>\n", eventsErr)
	case len(events) == 0:
		fmt.Fprintln(w, "  <none>")
	default:
		sort.SliceStable(events, func(i, j int) bool {
			return eventTime(events[i]).Before(eventTime(events[j]))
		})
		fmt.Fprintln(w, "  Last Seen\tType\tReason\tFrom\tMessage")
		for _, ev := range events {
			from := ev.Source.Component
			if from == "" {
				from = ev.ReportingController
			}
			reason := ev.Reason
			if ev.Count > 1 {
				reason = fmt.Sprintf("%s (x%d)", reason, ev.Count)
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
				age(eventTime(ev)), ev.Type, reason, orNone(from), strings.TrimSpace(ev.Message))
		}
	}

	w.Flush()
	return b.String()
}

func writeContainers(w io.Writer, specs []corev1.Container, statuses []corev1.ContainerStatus, age func(time.Time) string) {
	byName := make(map[string]corev1.ContainerStatus, len(statuses))
	for _, s := range statuses {
		byName[s.Name] = s
	}
	for _, c := range specs {
		fmt.Fprintf(w, "  %s:\n", c.Name)
		fmt.Fprintf(w, "    Image:\t%s\n", c.Image)
		s, ok := byName[c.Name]
		if !ok {
			fmt.Fprintf(w, "    State:\t<unknown>\n")
			continue
		}
		fmt.Fprintf(w, "    State:\t%s\n", formatState(s.State, age))
		if s.LastTerminationState.Terminated != nil {
			fmt.Fprintf(w, "    Last State:\t%s\n", formatState(s.LastTerminationState, age))
		}
		fmt.Fprintf(w, "    Ready:\t%t\n", s.Ready)
		fmt.Fprintf(w, "    Restart Count:\t%d\n", s.RestartCount)
	}
}

func formatState(s corev1.ContainerState, age func(time.Time) string) string {
	switch {
	case s.Running != nil:
		return fmt.Sprintf("Running (started %s ago)", age(s.Running.StartedAt.Time))
	case s.Waiting != nil:
		out := "Waiting"
		if s.Waiting.Reason != "" {
			out += " (" + s.Waiting.Reason + ")"
		}
		if s.Waiting.Message != "" {
			out += ": " + s.Waiting.Message
		}
		return out
	case s.Terminated != nil:
		t := s.Terminated
		return fmt.Sprintf("Terminated (%s, exit code %d, %s ago)", orNone(t.Reason), t.ExitCode, age(t.FinishedAt.Time))
	}
	return "<unknown>"
}

// eventTime picks the most meaningful timestamp of an event.
func eventTime(ev corev1.Event) time.Time {
	switch {
	case !ev.LastTimestamp.IsZero():
		return ev.LastTimestamp.Time
	case !ev.EventTime.IsZero():
		return ev.EventTime.Time
	case !ev.FirstTimestamp.IsZero():
		return ev.FirstTimestamp.Time
	}
	return ev.CreationTimestamp.Time
}

func field(w io.Writer, name, value string) {
	fmt.Fprintf(w, "%s:\t%s\n", name, value)
}

func formatMap(m map[string]string) string {
	if len(m) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + m[k]
	}
	return strings.Join(pairs, ",")
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}
