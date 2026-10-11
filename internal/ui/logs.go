package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ducnt114/kboba/internal/k8s"
)

// maxLogLines is how many log lines are kept in memory per stream.
const maxLogLines = 5000

// Like the resource watch, every log message carries a generation so lines
// from a stream we already left are dropped.
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
	Up, Down, Follow, Search, Next, Prev, Wrap, Timestamps, Previous, Container, Scroll key.Binding
}

var logsKeyMap = logsKeys{
	Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "scroll")),
	Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "scroll")),
	Follow:     key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
	Search:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Next:       key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next match")),
	Prev:       key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "prev match")),
	Wrap:       key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wrap")),
	Timestamps: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "timestamps")),
	Previous:   key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "previous")),
	Container:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "next container")),
	Scroll:     key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "scroll")),
}

// logsView follows the logs of one container.
type logsView struct {
	viewport viewport.Model
	buf      ringBuffer

	client    k8s.Client
	pod       k8s.PodInfo
	container string
	opts      k8s.LogOptions // timestamps / previous; changing them restarts the stream

	// follow keeps the view scrolled to the newest line. The stream itself
	// always keeps running.
	follow bool
	wrap   bool
	ended  bool

	// Search. term is lower-case; matches holds the absolute line numbers
	// (see ringBuffer.dropped) of lines containing it; current is the one
	// we jumped to, or -1.
	search  textinput.Model
	term    string
	matches []int
	current int
	// lineOffsets[i] is the viewport line where buffered line i starts
	// (they differ from i once wrapped lines take several rows).
	lineOffsets []int

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
	vp.SetHorizontalStep(4)

	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "search"

	return logsView{viewport: vp, buf: newRingBuffer(maxLogLines), search: ti, current: -1}
}

// open starts following pod's default container.
func (v *logsView) open(c k8s.Client, pod k8s.PodInfo) tea.Cmd {
	v.client = c
	v.pod = pod
	v.container = pod.DefaultContainer
	v.opts = k8s.LogOptions{}
	v.term = ""
	return v.restart()
}

// restart (re)starts the stream for the current container and options
// from scratch.
func (v *logsView) restart() tea.Cmd {
	v.stop()
	v.gen++
	v.buf.reset()
	v.follow = true
	v.ended = false
	v.current = -1
	v.render()

	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel

	c, pod, container, opts, gen := v.client, v.pod, v.container, v.opts, v.gen
	return func() tea.Msg {
		checkCtx, cancelCheck := context.WithTimeout(ctx, requestTimeout)
		err := k8s.CheckAccess(checkCtx, c, k8s.LogsAccess(pod.Namespace))
		cancelCheck()
		if err != nil {
			return logStreamStartedMsg{gen: gen, err: err}
		}
		lines, errs, err := c.StreamLogs(ctx, pod.Namespace, pod.Name, container, opts)
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
		// One render per batch, not per line.
		v.render()
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
		if v.search.Focused() {
			return v.handleSearchKey(msg)
		}
		return v.handleKey(msg)
	}
	return v, nil
}

func (v logsView) handleSearchKey(msg tea.KeyMsg) (logsView, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.search.Blur()
		v.search.SetValue("")
		v.term = ""
		v.current = -1
		v.render()
		return v, nil
	case tea.KeyEnter:
		v.search.Blur()
		v.term = strings.ToLower(v.search.Value())
		v.current = -1
		v.render()
		return v.jump(true), nil
	}
	var cmd tea.Cmd
	v.search, cmd = v.search.Update(msg)
	return v, cmd
}

func (v logsView) handleKey(msg tea.KeyMsg) (logsView, tea.Cmd) {
	switch {
	case key.Matches(msg, globalKeyMap.Back):
		if v.term != "" { // first esc clears the search, the next one leaves
			v.term = ""
			v.search.SetValue("")
			v.current = -1
			v.render()
			return v, nil
		}
		return v, goBack
	case key.Matches(msg, logsKeyMap.Follow):
		v.follow = !v.follow
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, nil
	case key.Matches(msg, logsKeyMap.Search):
		return v, v.search.Focus()
	case key.Matches(msg, logsKeyMap.Next) && v.term != "":
		return v.jump(true), nil
	case key.Matches(msg, logsKeyMap.Prev) && v.term != "":
		return v.jump(false), nil
	case key.Matches(msg, logsKeyMap.Wrap):
		v.wrap = !v.wrap
		v.viewport.SetXOffset(0)
		v.render()
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, nil
	case key.Matches(msg, logsKeyMap.Timestamps):
		v.opts.Timestamps = !v.opts.Timestamps
		return v, v.restart()
	case key.Matches(msg, logsKeyMap.Previous):
		v.opts.Previous = !v.opts.Previous
		return v, v.restart()
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

// jump scrolls to the next (or previous) search match.
func (v logsView) jump(forward bool) logsView {
	m, ok := nextMatch(v.matches, v.current, v.topLine(), forward)
	if !ok {
		return v
	}
	v.current = m
	v.follow = false
	if i := m - v.buf.dropped; i >= 0 && i < len(v.lineOffsets) {
		v.viewport.SetYOffset(v.lineOffsets[i])
	}
	return v
}

// topLine is the absolute number of the buffered line at the top of the
// viewport.
func (v logsView) topLine() int {
	i := sort.SearchInts(v.lineOffsets, v.viewport.YOffset+1) - 1
	return v.buf.dropped + max(i, 0)
}

func nextContainer(containers []string, current string) string {
	for i, c := range containers {
		if c == current {
			return containers[(i+1)%len(containers)]
		}
	}
	return containers[0]
}

// render rebuilds the viewport content from the buffer: highlights search
// matches, wraps if enabled, and records where each line starts. It runs
// once per batch of lines, not once per line.
func (v *logsView) render() {
	v.matches = v.matches[:0]
	v.lineOffsets = v.lineOffsets[:0]

	var b strings.Builder
	row := 0
	for i := range v.buf.len() {
		line := strings.ReplaceAll(v.buf.at(i), "\t", "    ")
		if containsFold(line, v.term) {
			v.matches = append(v.matches, v.buf.dropped+i)
			line = highlightMatches(line, v.term, matchStyle)
		}
		if v.wrap && v.viewport.Width > 0 {
			line = ansi.Hardwrap(line, v.viewport.Width, true)
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		v.lineOffsets = append(v.lineOffsets, row)
		row += strings.Count(line, "\n") + 1
		b.WriteString(line)
	}
	v.viewport.SetContent(b.String())
}

func (v *logsView) SetSize(w, h int) {
	v.viewport.Width = w
	v.viewport.Height = max(h-1, 1) // one line for the title
	v.search.Width = w - 2
	if v.wrap {
		v.render() // wrapping depends on the width
	}
	if v.follow {
		v.viewport.GotoBottom()
	}
}

func (v logsView) View() string {
	title := titleStyle.Render(fmt.Sprintf("Logs %s/%s [%s]", v.pod.Namespace, v.pod.Name, v.container))

	flags := []string{"follow:" + onOff(v.follow), "wrap:" + onOff(v.wrap), "ts:" + onOff(v.opts.Timestamps)}
	if v.opts.Previous {
		flags = append(flags, "PREVIOUS")
	}
	flags = append(flags, fmt.Sprintf("lines:%d", v.buf.len()))
	if v.ended {
		flags = append(flags, "(stream closed)")
	}
	info := "  " + strings.Join(flags, "  ")

	switch {
	case v.search.Focused():
		info += "  " + v.search.View()
	case v.term != "":
		info += fmt.Sprintf("  /%s %s", v.term, v.matchPosition())
	}
	return lipgloss.JoinVertical(lipgloss.Left, title+statusStyle.Render(info), v.viewport.View())
}

// matchPosition renders "3/17" (or "0/17" before the first jump).
func (v logsView) matchPosition() string {
	pos := 0
	for i, m := range v.matches {
		if m == v.current {
			pos = i + 1
		}
	}
	return fmt.Sprintf("%d/%d", pos, len(v.matches))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (v logsView) capturingInput() bool { return v.search.Focused() }

func (v logsView) keys() []key.Binding {
	k := logsKeyMap
	b := []key.Binding{k.Up, k.Down, k.Follow, k.Search}
	if v.term != "" {
		b = append(b, k.Next, k.Prev)
	}
	b = append(b, k.Wrap, k.Timestamps, k.Previous)
	if len(v.pod.Containers) > 1 {
		b = append(b, k.Container)
	}
	if !v.wrap {
		b = append(b, k.Scroll)
	}
	return append(b, globalKeyMap.Back)
}
