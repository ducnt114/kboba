package ui

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
	"github.com/ducnt114/kboba/internal/logmux"
)

// sourceSep separates the pod name from the text in aggregated log lines.
// Lines are stored as plain "pod │ text"; the pod name is coloured only at
// render time, so search works on plain text.
const sourceSep = " │ "

// multiLogs is the state of an aggregated log view: the logs of every pod
// matching a label selector, like `stern` or `kubectl logs -l`. The pod set
// is live: a watch on the pods starts streams for new pods (a rollout) and
// stops them for deleted ones.
type multiLogs struct {
	title     string // e.g. "deployments/web"
	namespace string
	selector  string

	ctx     context.Context // parent of every pod stream; cancelled by stop()
	mux     *logmux.Mux
	watch   *k8s.ResourceWatch
	pods    map[string]k8s.PodInfo
	streams map[string]context.CancelFunc // pods with a running stream
	// subscribed: a waitForMuxLines command is outstanding. The first one
	// is issued when the first stream joins, so no goroutine waits on an
	// empty mux; after that each batch re-subscribes.
	subscribed bool
	lastErr map[string]string             // to avoid repeating the same error line
}

type (
	openMultiLogsMsg struct {
		title, namespace, selector string
	}
	logPodsWatchMsg struct {
		watch *k8s.ResourceWatch
		gen   int
		err   error
	}
	logPodEventsMsg struct {
		events []k8s.ResourceEvent
		gen    int
	}
	logPodStreamMsg struct {
		pod   string
		lines <-chan string
		errs  <-chan error
		gen   int
		err   error
	}
	logMuxLinesMsg struct {
		lines []logmux.Line
		gen   int
	}
)

// openMulti starts following every pod selected by selector in namespace.
func (v *logsView) openMulti(c k8s.Client, title, namespace, selector string) tea.Cmd {
	v.client = c
	v.pod = k8s.PodInfo{}
	v.container = ""
	v.opts = k8s.LogOptions{}
	v.term = ""
	v.multi = &multiLogs{title: title, namespace: namespace, selector: selector}
	return v.restart()
}

// restartMulti is restart() for aggregated logs: a fresh mux and pod watch.
func (v *logsView) restartMulti(ctx context.Context) tea.Cmd {
	m := v.multi
	m.ctx = ctx
	m.mux = logmux.New(ctx)
	m.watch = nil
	m.pods = map[string]k8s.PodInfo{}
	m.streams = map[string]context.CancelFunc{}
	m.lastErr = map[string]string{}
	m.subscribed = false

	c, ns, sel, gen := v.client, m.namespace, m.selector, v.gen
	watch := func() tea.Msg {
		checkCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		err := k8s.CheckAccess(checkCtx, c,
			k8s.LogsAccess(ns), k8s.Pods.Access("list", ns), k8s.Pods.Access("watch", ns))
		if err != nil {
			return logPodsWatchMsg{gen: gen, err: err}
		}
		w, err := c.WatchResources(k8s.Pods, ns, sel)
		return logPodsWatchMsg{watch: w, gen: gen, err: err}
	}
	return watch
}

func waitForLogPods(w *k8s.ResourceWatch, gen int) tea.Cmd {
	return func() tea.Msg {
		events, ok := receiveBatch(w.Events, 500)
		if !ok {
			return nil // stopped: we left this view or restarted
		}
		return logPodEventsMsg{events: events, gen: gen}
	}
}

func waitForMuxLines(mux *logmux.Mux, gen int) tea.Cmd {
	return func() tea.Msg {
		lines, ok := receiveBatch(mux.Lines(), 1000)
		if !ok {
			return nil // the mux was closed by stop()
		}
		return logMuxLinesMsg{lines: lines, gen: gen}
	}
}

// streamable reports whether a pod's container has started, so asking for
// its logs makes sense.
func streamable(p k8s.PodInfo) bool {
	switch {
	case p.Status == "Pending", p.Status == "ContainerCreating", p.Status == "PodInitializing":
		return false
	case strings.HasPrefix(p.Status, "Init:"):
		return false
	}
	return true
}

// startPodStream opens the log stream of one pod (its default container).
func (v *logsView) startPodStream(p k8s.PodInfo) tea.Cmd {
	m := v.multi
	ctx, cancel := context.WithCancel(m.ctx)
	m.streams[p.Name] = cancel
	c, opts, gen := v.client, v.opts, v.gen
	return func() tea.Msg {
		lines, errs, err := c.StreamLogs(ctx, p.Namespace, p.Name, p.DefaultContainer, opts)
		return logPodStreamMsg{pod: p.Name, lines: lines, errs: errs, gen: gen, err: err}
	}
}

// updateMulti handles the messages of an aggregated log view.
func (v logsView) updateMulti(msg tea.Msg) (logsView, tea.Cmd) {
	m := v.multi
	switch msg := msg.(type) {
	case logPodsWatchMsg:
		if msg.gen != v.gen {
			if msg.watch != nil {
				msg.watch.Stop() // started after we moved on: don't leak it
			}
			return v, nil
		}
		if msg.err != nil {
			v.ended = true
			return v, reportErr(msg.err)
		}
		m.watch = msg.watch
		return v, waitForLogPods(m.watch, v.gen)

	case logPodEventsMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		var cmds []tea.Cmd
		for _, ev := range msg.events {
			switch ev.Type {
			case k8s.Upserted:
				if ev.Resource.Pod == nil {
					continue
				}
				p := *ev.Resource.Pod
				m.pods[p.Name] = p
				// Also restarts a stream that ended (container restarted).
				if _, running := m.streams[p.Name]; !running && streamable(p) {
					cmds = append(cmds, v.startPodStream(p))
				}
			case k8s.Deleted:
				if cancel, ok := m.streams[ev.Resource.Name]; ok {
					cancel()
					delete(m.streams, ev.Resource.Name)
				}
				delete(m.pods, ev.Resource.Name)
			case k8s.WatchFailed:
				cmds = append(cmds, reportErr(ev.Err))
			}
		}
		cmds = append(cmds, waitForLogPods(m.watch, v.gen))
		return v, tea.Batch(cmds...)

	case logPodStreamMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		if msg.err != nil {
			// Let a later pod update retry; show the error once.
			delete(m.streams, msg.pod)
			v.noteSource(msg.pod, "⚠ "+msg.err.Error())
			return v, nil
		}
		m.mux.Add(msg.pod, msg.lines, msg.errs)
		if m.subscribed {
			return v, nil
		}
		m.subscribed = true
		return v, waitForMuxLines(m.mux, v.gen)

	case logMuxLinesMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		for _, l := range msg.lines {
			switch {
			case l.Ended && l.Err != nil:
				delete(m.streams, l.Source)
				v.noteSource(l.Source, "⚠ "+l.Err.Error())
			case l.Ended:
				delete(m.streams, l.Source)
				v.noteSource(l.Source, "— stream ended —")
			default:
				delete(m.lastErr, l.Source)
				v.buf.push(l.Source + sourceSep + l.Text)
			}
		}
		v.render()
		if v.follow {
			v.viewport.GotoBottom()
		}
		return v, waitForMuxLines(m.mux, v.gen)
	}
	return v, nil
}

// noteSource adds a status line for a pod, unless it repeats the last one
// (a crash-looping pod would otherwise print the same error every retry).
func (v *logsView) noteSource(pod, note string) {
	if v.multi.lastErr[pod] == note {
		return
	}
	v.multi.lastErr[pod] = note
	v.buf.push(pod + sourceSep + note)
	v.render()
}

// stopMulti stops the pod watch; cancelling the view's context (done by
// stop) ends the mux and every pod stream.
func (m *multiLogs) stop() {
	if m != nil && m.watch != nil {
		m.watch.Stop()
		m.watch = nil
	}
}

// renderSource colours the "pod │ " prefix of an aggregated log line.
func renderSource(line string, highlight func(string) string) string {
	pod, text, ok := strings.Cut(line, sourceSep)
	if !ok {
		return highlight(line)
	}
	return sourceStyle(pod).Render(pod) + statusStyle.Render(sourceSep) + highlight(text)
}

var sourcePalette = []lipgloss.AdaptiveColor{
	{Light: "#0077BB", Dark: "#33BBEE"},
	{Light: "#EE7733", Dark: "#EE7733"},
	{Light: "#009988", Dark: "#44DDAA"},
	{Light: "#CC3311", Dark: "#FF7766"},
	{Light: "#AA3377", Dark: "#EE88CC"},
	{Light: "#777700", Dark: "#DDCC77"},
}

// sourceStyle gives each pod a stable colour: the same name always hashes
// to the same palette entry.
func sourceStyle(pod string) lipgloss.Style {
	h := fnv.New32a()
	h.Write([]byte(pod))
	return lipgloss.NewStyle().Foreground(sourcePalette[h.Sum32()%uint32(len(sourcePalette))])
}

// titleText is the logs view title in aggregated mode.
func (m *multiLogs) titleText() string {
	return fmt.Sprintf("Logs %s (%d pods, %d streaming)", m.title, len(m.pods), len(m.streams))
}
