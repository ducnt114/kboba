package ui

import (
	"fmt"
	"sort"
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
// When the resource type, namespace or context changes the generation is
// bumped, so messages still in flight from the old watch are dropped.
type (
	watchStartedMsg struct {
		watch *k8s.ResourceWatch
		gen   int
		err   error
	}
	resourceEventsMsg struct {
		events []k8s.ResourceEvent
		gen    int
	}
	watchClosedMsg struct{ gen int }
	ageTickMsg     struct{}
)

// resourceQuery is what a resources view shows. The navigation stack
// saves these, so going back restores exactly what was on screen.
type resourceQuery struct {
	rt        *k8s.ResourceType
	namespace string
	// selector restricts the list to objects with matching labels ("" for
	// none); scope says where it came from, e.g. "deployments/web".
	selector string
	scope    string
}

// openLogsMsg / openDescribeMsg / openYAMLMsg ask the root model to open
// another view for the selected row; drillDownMsg asks it to show the pods
// selected by the row (a deployment's or service's pods).
type (
	drillDownMsg struct {
		rt       *k8s.ResourceType
		resource k8s.Resource
	}
	openLogsMsg     struct{ pod k8s.PodInfo }
	openDescribeMsg struct{ pod k8s.PodInfo }
	openYAMLMsg     struct {
		rt       *k8s.ResourceType
		resource k8s.Resource
	}
)

type resourceKeys struct {
	Up, Down, Logs, Drill, Describe, YAML, Filter key.Binding
}

var resourceKeyMap = resourceKeys{
	Up:       listKeyMap.Up,
	Down:     listKeyMap.Down,
	Logs:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "logs")),
	Drill:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "pods")),
	Describe: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
	YAML:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yaml")),
	Filter:   listKeyMap.Filter,
}

// resourcesView is a live table of one resource type (pods, deployments,
// ...), fed by an informer. What differs between types — the columns and
// how an object becomes a row — comes from k8s.ResourceType.
type resourcesView struct {
	table  table.Model
	filter textinput.Model

	q       resourceQuery
	items   map[string]k8s.Resource
	rowKeys []string // item key for each table row, same order
	synced  bool
	// pendingSelect is a row to put the cursor on once it shows up (after
	// going back, the restarted watch delivers the rows again).
	pendingSelect string

	watch *k8s.ResourceWatch
	gen   int

	width int
	now   func() time.Time // injectable for tests
}

func newResourcesView() resourcesView {
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

	return resourcesView{
		table:  t,
		filter: ti,
		q:      resourceQuery{rt: k8s.Pods},
		items:  map[string]k8s.Resource{},
		now:    time.Now,
	}
}

// start stops the current watch (if any) and starts watching what q
// describes. selectKey (may be empty) is the row to select once it arrives.
func (v *resourcesView) start(c k8s.Client, q resourceQuery, selectKey string) tea.Cmd {
	v.stop()
	v.gen++
	if v.q.rt != q.rt {
		v.filter.SetValue("") // a pod filter rarely makes sense for nodes
	}
	v.q = q
	v.pendingSelect = selectKey
	v.items = map[string]k8s.Resource{}
	v.synced = false
	v.setColumns()
	v.refreshRows()

	gen := v.gen
	return func() tea.Msg {
		w, err := c.WatchResources(q.rt, q.namespace, q.selector)
		return watchStartedMsg{watch: w, gen: gen, err: err}
	}
}

// stop shuts the informer down. Its Events channel then closes, which
// releases the tea.Cmd waiting on it.
func (v *resourcesView) stop() {
	if v.watch != nil {
		v.watch.Stop()
		v.watch = nil
	}
}

// waitForResourceEvents is the "wait for the next message" half of the
// subscription; Update re-issues it after every batch.
func waitForResourceEvents(w *k8s.ResourceWatch, gen int) tea.Cmd {
	return func() tea.Msg {
		events, ok := receiveBatch(w.Events, 500)
		if !ok {
			return watchClosedMsg{gen: gen}
		}
		return resourceEventsMsg{events: events, gen: gen}
	}
}

func ageTick() tea.Cmd {
	return tea.Tick(ageRefreshInterval, func(time.Time) tea.Msg { return ageTickMsg{} })
}

func (v resourcesView) Update(msg tea.Msg) (resourcesView, tea.Cmd) {
	switch msg := msg.(type) {
	case watchStartedMsg:
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
		return v, waitForResourceEvents(msg.watch, msg.gen)

	case resourceEventsMsg:
		if msg.gen != v.gen {
			return v, nil // stale: from a watch we already stopped
		}
		var status tea.Cmd
		for _, ev := range msg.events {
			switch ev.Type {
			case k8s.Upserted:
				v.items[ev.Resource.Key()] = ev.Resource
			case k8s.Deleted:
				delete(v.items, ev.Resource.Key())
			case k8s.Synced:
				v.synced = true
				status = reportInfo(fmt.Sprintf("watching %d %s", len(v.items), v.q.rt.Name))
			case k8s.WatchFailed:
				status = reportErr(ev.Err)
			}
		}
		v.refreshRows()
		if v.synced {
			v.pendingSelect = "" // the row is gone; don't jump to it later
		}
		return v, tea.Batch(status, waitForResourceEvents(v.watch, v.gen))

	case watchClosedMsg:
		return v, nil

	case ageTickMsg:
		v.refreshRows()
		return v, ageTick()

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return v, nil
}

func (v resourcesView) handleKey(msg tea.KeyMsg) (resourcesView, tea.Cmd) {
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
	case key.Matches(msg, resourceKeyMap.Filter):
		return v, v.filter.Focus()
	case key.Matches(msg, globalKeyMap.Back):
		if v.filter.Value() != "" {
			v.filter.SetValue("")
			v.refreshRows()
			return v, nil
		}
		return v, goBack // the root model pops the navigation stack
	case key.Matches(msg, resourceKeyMap.Drill) && v.q.rt != k8s.Pods:
		if r, ok := v.selected(); ok && r.Selector != "" {
			rt := v.q.rt
			return v, func() tea.Msg { return drillDownMsg{rt: rt, resource: r} }
		}
		return v, nil
	case key.Matches(msg, resourceKeyMap.Logs) && v.q.rt == k8s.Pods:
		if p, ok := v.selectedPod(); ok {
			return v, func() tea.Msg { return openLogsMsg{pod: p} }
		}
		return v, nil
	case key.Matches(msg, resourceKeyMap.Describe) && v.q.rt == k8s.Pods:
		if p, ok := v.selectedPod(); ok {
			return v, func() tea.Msg { return openDescribeMsg{pod: p} }
		}
		return v, nil
	case key.Matches(msg, resourceKeyMap.YAML):
		if r, ok := v.selected(); ok {
			rt := v.q.rt
			return v, func() tea.Msg { return openYAMLMsg{rt: rt, resource: r} }
		}
		return v, nil
	}

	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return v, cmd
}

func (v resourcesView) selected() (k8s.Resource, bool) {
	r, ok := v.items[v.selectedKey()]
	return r, ok
}

func (v resourcesView) selectedPod() (k8s.PodInfo, bool) {
	r, ok := v.selected()
	if !ok || r.Pod == nil {
		return k8s.PodInfo{}, false
	}
	return *r.Pod, true
}

// showNamespace reports whether the NAMESPACE column is shown.
func (v resourcesView) showNamespace() bool {
	return v.q.rt.Namespaced && v.q.namespace == allNamespaces
}

// setColumns builds the table columns: [NAMESPACE] + the type's own columns
// + AGE. The flexible column (Width 0) takes the remaining width.
func (v *resourcesView) setColumns() {
	var cols []table.Column
	if v.showNamespace() {
		cols = append(cols, table.Column{Title: "NAMESPACE", Width: 20})
	}
	for _, c := range v.q.rt.Columns {
		cols = append(cols, table.Column{Title: c.Title, Width: c.Width})
	}
	cols = append(cols, table.Column{Title: "AGE", Width: 7})

	used, flex := 0, -1
	for i, c := range cols {
		used += c.Width + 2 // cell padding
		if c.Width == 0 {
			flex = i
		}
	}
	if flex >= 0 {
		cols[flex].Width = max(v.width-used-2, 20)
	}

	// Rows must match the column count before columns change.
	v.table.SetRows(nil)
	v.table.SetColumns(cols)
}

// refreshRows rebuilds the table rows from the item map, applying the
// filter and keeping the cursor on the same item when possible.
func (v *resourcesView) refreshRows() { v.setRows(v.selectedKey()) }

func (v resourcesView) selectedKey() string {
	if i := v.table.Cursor(); i >= 0 && i < len(v.rowKeys) {
		return v.rowKeys[i]
	}
	return ""
}

func (v *resourcesView) setRows(selectedKey string) {
	if v.pendingSelect != "" {
		selectedKey = v.pendingSelect
	}
	filter := strings.ToLower(v.filter.Value())
	keys := make([]string, 0, len(v.items))
	for k, r := range v.items {
		if filter == "" || strings.Contains(strings.ToLower(r.Name), filter) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys) // "namespace/name" sorts by namespace, then name

	now := v.now()
	rows := make([]table.Row, len(keys))
	cursor := 0
	for i, k := range keys {
		r := v.items[k]
		row := make(table.Row, 0, len(r.Cells)+2)
		if v.showNamespace() {
			row = append(row, r.Namespace)
		}
		row = append(row, r.Cells...)
		row = append(row, formatAge(now.Sub(r.Created)))
		rows[i] = row
		if k == selectedKey {
			cursor = i
			if k == v.pendingSelect {
				v.pendingSelect = ""
			}
		}
	}

	v.rowKeys = keys
	v.table.SetRows(rows)
	v.table.SetCursor(cursor)
}

func (v *resourcesView) SetSize(w, h int) {
	v.width = w
	v.filter.Width = w - 2
	v.table.SetWidth(w)
	v.table.SetHeight(max(h-1, 1)) // one line for the title
	selected := v.selectedKey()
	v.setColumns()
	v.setRows(selected)
}

func (v resourcesView) View() string {
	var label string
	switch {
	case !v.q.rt.Namespaced:
		label = v.q.rt.Title
	case v.q.namespace == allNamespaces:
		label = v.q.rt.Title + "(all)"
	default:
		label = fmt.Sprintf("%s(%s)", v.q.rt.Title, v.q.namespace)
	}
	title := titleStyle.Render(fmt.Sprintf("%s[%d]", label, len(v.rowKeys)))
	if v.q.scope != "" {
		title += statusStyle.Render("  ← " + v.q.scope)
	}
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

func (v resourcesView) capturingInput() bool { return v.filter.Focused() }

func (v resourcesView) keys() []key.Binding {
	k := resourceKeyMap
	b := []key.Binding{k.Up, k.Down}
	switch v.q.rt {
	case k8s.Pods:
		b = append(b, k.Logs, k.Describe)
	case k8s.Deployments, k8s.Services:
		b = append(b, k.Drill)
	}
	b = append(b, k.YAML, k.Filter)
	if v.filter.Value() != "" {
		b = append(b, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")))
	}
	return b
}
