package ui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
)

// maxLogLines is how many log lines are kept in memory per stream.
const maxLogLines = 5000

// Like the pod watch, every log message carries a generation so lines from
// a stream we already left are dropped.
type (
	logStreamStartedMsg struct {
		lines <-chan string
		errs  <-chan error
		gen   int
		err   error
	}
	logLinesMsg struct {
		lines []string
		gen   int
	}
	logStreamEndedMsg struct {
		gen int
		err error
	}
)

type logsKeys struct {
	Up, Down, Follow, Container key.Binding
}

var logsKeyMap = logsKeys{
	Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "scroll")),
	Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "scroll")),
	Follow:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
	Container: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "next container")),
}

// logsView follows the logs of one container.
type logsView struct {
	viewport viewport.Model
	buf      ringBuffer

	client    k8s.Client
	pod       k8s.PodInfo
	container string

	// follow keeps the view scrolled to the newest line. The stream itself
	// always keeps running.
	follow bool
	ended  bool

	// The current stream's channels, kept so Update can re-subscribe.
	lines  <-chan string
	errs   <-chan error
	cancel context.CancelFunc
	gen    int
}

func newLogsView() logsView {
	vp := viewport.New(0, 0)
	// "f" is ours (follow); keep the rest of the default bindings.
	vp.KeyMap.PageDown = key.NewBinding(key.WithKeys("pgdown", " "))
	return logsView{viewport: vp, buf: newRingBuffer(maxLogLines)}
}

// open starts following pod's default container.
func (v *logsView) open(c k8s.Client, pod k8s.PodInfo) tea.Cmd {
	v.client = c
	v.pod = pod
	v.container = pod.DefaultContainer
	return v.restart()
}

// restart (re)starts the stream for the current container from scratch.
func (v *logsView) restart() tea.Cmd {
	v.stop()
	v.gen++
	v.buf.reset()
	v.follow = true
	v.ended = false
	v.viewport.SetContent("")

	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel

	c, pod, container, gen := v.client, v.pod, v.container, v.gen
	return func() tea.Msg {
		lines, errs, err := c.StreamLogs(ctx, pod.Namespace, pod.Name, container)
		return logStreamStartedMsg{lines: lines, errs: errs, gen: gen, err: err}
	}
}

// stop cancels the stream: the HTTP connection is closed and the reader
// goroutine exits, closing its channel.
func (v *logsView) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.lines, v.errs = nil, nil
}

func waitForLogLines(lines <-chan string, errs <-chan error, gen int) tea.Cmd {
	return func() tea.Msg {
		batch, ok := receiveBatch(lines, 1000)
		if ok {
			return logLinesMsg{lines: batch, gen: gen}
		}
		// The producer sends its error (if any) before closing lines.
		select {
		case err := <-errs:
			return logStreamEndedMsg{gen: gen, err: err}
		default:
			return logStreamEndedMsg{gen: gen}
		}
	}
}

func (v logsView) Update(msg tea.Msg) (logsView, tea.Cmd) {
	switch msg := msg.(type) {
	case logStreamStartedMsg:
		if msg.gen != v.gen {
			return v, nil // already cancelled; its goroutine winds down by itself
		}
		if msg.err != nil {
			v.ended = true
			return v, reportErr(msg.err)
		}
		v.lines, v.errs = msg.lines, msg.errs
		return v, waitForLogLines(v.lines, v.errs, v.gen)

	case logLinesMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		for _, l := range msg.lines {
			v.buf.push(l)
		}
		// One join per batch, not per line.
		v.viewport.SetContent(v.buf.String())
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, waitForLogLines(v.lines, v.errs, v.gen)

	case logStreamEndedMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		v.ended = true
		if msg.err != nil {
			return v, reportErr(fmt.Errorf("log stream: %w", msg.err))
		}
		return v, reportInfo("log stream closed")

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return v, nil
}

func (v logsView) handleKey(msg tea.KeyMsg) (logsView, tea.Cmd) {
	switch {
	case key.Matches(msg, globalKeyMap.Back):
		return v, goBack
	case key.Matches(msg, logsKeyMap.Follow):
		v.follow = !v.follow
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, nil
	case key.Matches(msg, logsKeyMap.Container):
		if len(v.pod.Containers) < 2 {
			return v, nil
		}
		v.container = nextContainer(v.pod.Containers, v.container)
		return v, v.restart()
	}

	var cmd tea.Cmd
	v.viewport, cmd = v.viewport.Update(msg)
	// Scrolling away from the bottom pauses follow.
	if !v.viewport.AtBottom() {
		v.follow = false
	}
	return v, cmd
}

func nextContainer(containers []string, current string) string {
	for i, c := range containers {
		if c == current {
			return containers[(i+1)%len(containers)]
		}
	}
	return containers[0]
}

func (v *logsView) SetSize(w, h int) {
	v.viewport.Width = w
	v.viewport.Height = max(h-1, 1) // one line for the title
	if v.follow {
		v.viewport.GotoBottom()
	}
}

func (v logsView) View() string {
	follow := "off"
	if v.follow {
		follow = "on"
	}
	title := titleStyle.Render(fmt.Sprintf("Logs %s/%s [%s]", v.pod.Namespace, v.pod.Name, v.container))
	info := fmt.Sprintf("  follow:%s  lines:%d", follow, v.buf.len())
	if v.ended {
		info += "  (stream closed)"
	}
	return lipgloss.JoinVertical(lipgloss.Left, title+statusStyle.Render(info), v.viewport.View())
}

func (v logsView) capturingInput() bool { return false }

func (v logsView) keys() []key.Binding {
	k := logsKeyMap
	b := []key.Binding{k.Up, k.Down, k.Follow}
	if len(v.pod.Containers) > 1 {
		b = append(b, k.Container)
	}
	return append(b, globalKeyMap.Back)
}
