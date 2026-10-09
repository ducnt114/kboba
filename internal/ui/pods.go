package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
)

// ageRefreshInterval re-renders the AGE column. It is a UI timer only; the
// cluster is never polled.
const ageRefreshInterval = 5 * time.Second

// Every message produced by a watch carries the generation it belongs to.
// When the namespace or context changes the generation is bumped, so
// messages still in flight from the old watch are recognised and dropped.
type (
	podWatchStartedMsg struct {
		watch *k8s.PodWatch
		gen   int
		err   error
	}
	podEventsMsg struct {
		events []k8s.PodEvent
		gen    int
	}
	podWatchClosedMsg struct{ gen int }
	ageTickMsg        struct{}
)

// openLogsMsg / openDescribeMsg ask the root model to open a pod's view.
type (
	openLogsMsg     struct{ pod k8s.PodInfo }
	openDescribeMsg struct{ pod k8s.PodInfo }
)

type podsKeys struct {
	Up, Down, Logs, Describe, Filter key.Binding
}

var podsKeyMap = podsKeys{
	Up:       listKeyMap.Up,
	Down:     listKeyMap.Down,
	Logs:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "logs")),
	Describe: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
	Filter:   listKeyMap.Filter,
}

// podsView is a live table of pods, fed by an informer.
type podsView struct {
	table  table.Model
	filter textinput.Model

	namespace string
	pods      map[string]k8s.PodInfo
	rowKeys   []string // pod key for each table row, same order
	synced    bool

	watch *k8s.PodWatch
	gen   int

	width int
	now   func() time.Time // injectable for tests
}

func newPodsView() podsView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter by name"

	t := table.New(table.WithFocused(true))
	styles := table.DefaultStyles()
	styles.Header = styles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(colorMuted)
	styles.Selected = styles.Selected.Foreground(lipgloss.Color("#FFFFFF")).Background(colorAccent)
	t.SetStyles(styles)

	return podsView{
		table:  t,
		filter: ti,
		pods:   map[string]k8s.PodInfo{},
		now:    time.Now,
	}
}

// start stops the current watch (if any) and starts watching namespace.
func (v *podsView) start(c k8s.Client, namespace string) tea.Cmd {
	v.stop()
	v.gen++
	v.namespace = namespace
	v.pods = map[string]k8s.PodInfo{}
	v.synced = false
	v.setColumns()
	v.refreshRows()

	gen := v.gen
	return func() tea.Msg {
		w, err := c.WatchPods(namespace)
		return podWatchStartedMsg{watch: w, gen: gen, err: err}
	}
}

// stop shuts the informer down. Its Events channel then closes, which
// releases the tea.Cmd waiting on it.
func (v *podsView) stop() {
	if v.watch != nil {
		v.watch.Stop()
		v.watch = nil
	}
}

// waitForPodEvents is the "wait for the next message" half of the
// subscription; Update re-issues it after every batch.
func waitForPodEvents(w *k8s.PodWatch, gen int) tea.Cmd {
	return func() tea.Msg {
		events, ok := receiveBatch(w.Events, 500)
		if !ok {
			return podWatchClosedMsg{gen: gen}
		}
		return podEventsMsg{events: events, gen: gen}
	}
}

func ageTick() tea.Cmd {
	return tea.Tick(ageRefreshInterval, func(time.Time) tea.Msg { return ageTickMsg{} })
}

func (v podsView) Update(msg tea.Msg) (podsView, tea.Cmd) {
	switch msg := msg.(type) {
	case podWatchStartedMsg:
		if msg.gen != v.gen {
			// The user moved on before this watch started; don't leak it.
			if msg.watch != nil {
				msg.watch.Stop()
			}
			return v, nil
		}
		if msg.err != nil {
			return v, reportErr(msg.err)
		}
		v.watch = msg.watch
		return v, waitForPodEvents(msg.watch, msg.gen)

	case podEventsMsg:
		if msg.gen != v.gen {
			return v, nil // stale: from a watch we already stopped
		}
		var status tea.Cmd
		for _, ev := range msg.events {
			switch ev.Type {
			case k8s.PodUpserted:
				v.pods[ev.Pod.Key()] = ev.Pod
			case k8s.PodDeleted:
				delete(v.pods, ev.Pod.Key())
			case k8s.PodsSynced:
				v.synced = true
				status = reportInfo(fmt.Sprintf("watching %d pods", len(v.pods)))
			case k8s.PodWatchFailed:
				status = reportErr(ev.Err)
			}
		}
		v.refreshRows()
		return v, tea.Batch(status, waitForPodEvents(v.watch, v.gen))

	case podWatchClosedMsg:
		return v, nil

	case ageTickMsg:
		v.refreshRows()
		return v, ageTick()

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return v, nil
}

func (v podsView) handleKey(msg tea.KeyMsg) (podsView, tea.Cmd) {
	if v.filter.Focused() {
		switch msg.Type {
		case tea.KeyEsc:
			v.filter.SetValue("")
			v.filter.Blur()
		case tea.KeyEnter:
			v.filter.Blur()
		default:
			var cmd tea.Cmd
			v.filter, cmd = v.filter.Update(msg)
			v.refreshRows()
			return v, cmd
		}
		v.refreshRows()
		return v, nil
	}

	switch {
	case key.Matches(msg, podsKeyMap.Filter):
		return v, v.filter.Focus()
	case key.Matches(msg, globalKeyMap.Back):
		if v.filter.Value() != "" {
			v.filter.SetValue("")
			v.refreshRows()
		}
		return v, nil
	case key.Matches(msg, podsKeyMap.Logs):
		if p, ok := v.selected(); ok {
			return v, func() tea.Msg { return openLogsMsg{pod: p} }
		}
		return v, nil
	case key.Matches(msg, podsKeyMap.Describe):
		if p, ok := v.selected(); ok {
			return v, func() tea.Msg { return openDescribeMsg{pod: p} }
		}
		return v, nil
	}

	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return v, cmd
}

func (v podsView) selected() (k8s.PodInfo, bool) {
	p, ok := v.pods[v.selectedKey()]
	return p, ok
}

func (v podsView) allNamespaces() bool { return v.namespace == allNamespaces }

// setColumns sizes the columns for the current width. NAME takes whatever
// the fixed-width columns leave over.
func (v *podsView) setColumns() {
	fixed := []table.Column{
		{Title: "READY", Width: 7},
		{Title: "STATUS", Width: 22},
		{Title: "RESTARTS", Width: 9},
		{Title: "AGE", Width: 7},
	}
	var cols []table.Column
	if v.allNamespaces() {
		cols = append(cols, table.Column{Title: "NAMESPACE", Width: 20})
	}
	cols = append(cols, table.Column{Title: "NAME"})
	cols = append(cols, fixed...)

	used := 0
	for _, c := range cols {
		used += c.Width + 2 // cell padding
	}
	nameIdx := len(cols) - len(fixed) - 1
	cols[nameIdx].Width = max(v.width-used-2, 20)

	// Rows must match the column count before columns change.
	v.table.SetRows(nil)
	v.table.SetColumns(cols)
}

// refreshRows rebuilds the table rows from the pod map, applying the
// filter and keeping the cursor on the same pod when possible.
func (v *podsView) refreshRows() { v.setRows(v.selectedKey()) }

func (v podsView) selectedKey() string {
	if i := v.table.Cursor(); i >= 0 && i < len(v.rowKeys) {
		return v.rowKeys[i]
	}
	return ""
}

func (v *podsView) setRows(selectedKey string) {
	filter := strings.ToLower(v.filter.Value())
	keys := make([]string, 0, len(v.pods))
	for k, p := range v.pods {
		if filter == "" || strings.Contains(strings.ToLower(p.Name), filter) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys) // "namespace/name" sorts by namespace, then name

	now := v.now()
	rows := make([]table.Row, len(keys))
	cursor := 0
	for i, k := range keys {
		p := v.pods[k]
		row := table.Row{p.Name, p.Ready, p.Status, strconv.Itoa(int(p.Restarts)), formatAge(now.Sub(p.Created))}
		if v.allNamespaces() {
			row = append(table.Row{p.Namespace}, row...)
		}
		rows[i] = row
		if k == selectedKey {
			cursor = i
		}
	}

	v.rowKeys = keys
	v.table.SetRows(rows)
	v.table.SetCursor(cursor)
}

func (v *podsView) SetSize(w, h int) {
	v.width = w
	v.filter.Width = w - 2
	v.table.SetWidth(w)
	v.table.SetHeight(max(h-1, 1)) // one line for the title
	selected := v.selectedKey()
	v.setColumns()
	v.setRows(selected)
}

func (v podsView) View() string {
	ns := v.namespace
	if v.allNamespaces() {
		ns = "all"
	}
	title := titleStyle.Render(fmt.Sprintf("Pods(%s)[%d]", ns, len(v.rowKeys)))
	switch {
	case v.filter.Focused():
		title += "  " + v.filter.View()
	case v.filter.Value() != "":
		title += statusStyle.Render("  /" + v.filter.Value())
	case !v.synced:
		title += statusStyle.Render("  loading…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, v.table.View())
}

func (v podsView) capturingInput() bool { return v.filter.Focused() }

func (v podsView) keys() []key.Binding {
	k := podsKeyMap
	return []key.Binding{k.Up, k.Down, k.Logs, k.Describe, k.Filter}
}
