package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
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
	Up, Down, Logs, Drill, Describe, YAML, Filter, Sort, Reverse key.Binding
}

var resourceKeyMap = resourceKeys{
	Up:       listKeyMap.Up,
	Down:     listKeyMap.Down,
	Logs:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "logs")),
	Drill:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "pods")),
	Describe: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
	YAML:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yaml")),
	Filter:   listKeyMap.Filter,
	Sort:     key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort column")),
	Reverse:  key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
}

// resourcesView is a live table of one resource type (pods, deployments,
// ...), fed by an informer. What differs between types — the columns and
// how an object becomes a row — comes from k8s.ResourceType.
type resourcesView struct {
	table  tableModel
	filter textinput.Model

	q       resourceQuery
	items   map[string]k8s.Resource
	rowKeys []string // item key for each table row, same order
	synced  bool
	// pendingSelect is a row to put the cursor on once it shows up (after
	// going back, the restarted watch delivers the rows again).
	pendingSelect string

	// sortCol is the title of the column rows are sorted by; "" means the
	// default order (namespace, then name).
	sortCol  string
	sortDesc bool

	watch *k8s.ResourceWatch
	gen   int

	width int
	now   func() time.Time // injectable for tests
}

func newResourcesView() resourcesView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter by name"

	return resourcesView{
		table:  newTableModel(),
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
		// Filter and sort rarely make sense across types (other columns).
		v.filter.SetValue("")
		v.sortCol, v.sortDesc = "", false
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
	case key.Matches(msg, resourceKeyMap.Sort):
		v.sortCol = nextSortColumn(v.columnTitles(), v.sortCol)
		v.setColumns() // the arrow moves to another header
		v.refreshRows()
		return v, nil
	case key.Matches(msg, resourceKeyMap.Reverse):
		v.sortDesc = !v.sortDesc
		v.setColumns()
		v.refreshRows()
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

// columns returns [NAMESPACE] + the type's own columns + AGE.
func (v resourcesView) columns() []k8s.Column {
	var cols []k8s.Column
	if v.showNamespace() {
		cols = append(cols, k8s.Column{Title: "NAMESPACE", Width: 20})
	}
	cols = append(cols, v.q.rt.Columns...)
	return append(cols, k8s.Column{Title: "AGE", Width: 7})
}

func (v resourcesView) columnTitles() []string {
	var titles []string
	for _, c := range v.columns() {
		titles = append(titles, c.Title)
	}
	return titles
}

// nextSortColumn cycles through the titles, then back to the default
// order ("").
func nextSortColumn(titles []string, current string) string {
	for i, t := range titles {
		if t == current {
			if i+1 < len(titles) {
				return titles[i+1]
			}
			return ""
		}
	}
	return titles[0] // from the default order (or a column that's gone)
}

// setColumns builds the table columns. The flexible column (Width 0) takes
// the remaining width; the sorted column gets an arrow.
func (v *resourcesView) setColumns() {
	var cols []tableColumn
	for _, c := range v.columns() {
		title := c.Title
		if title == v.sortCol {
			title += sortArrow(v.sortDesc)
			if c.Width > 0 {
				c.Width = max(c.Width, lipgloss.Width(title)) // don't truncate the arrow away
			}
		}
		cols = append(cols, tableColumn{Title: title, Width: c.Width})
	}

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
	now := v.now()
	var entries []rowEntry
	for k, r := range v.items {
		if filter != "" && !strings.Contains(strings.ToLower(r.Name), filter) {
			continue
		}
		row := make([]string, 0, len(r.Cells)+2)
		if v.showNamespace() {
			row = append(row, r.Namespace)
		}
		row = append(row, r.Cells...)
		row = append(row, formatAge(now.Sub(r.Created)))
		entries = append(entries, rowEntry{key: k, res: r, row: row})
	}
	sortEntries(entries, slices.Index(v.columnTitles(), v.sortCol), v.sortCol == "AGE", v.sortDesc)

	keys := make([]string, len(entries))
	rows := make([][]string, len(entries))
	styles := make([]lipgloss.Style, len(entries))
	cursor := 0
	for i, e := range entries {
		keys[i], rows[i] = e.key, e.row
		styles[i] = healthStyles[rowHealth(v.q.rt, e.res)]
		if e.key == selectedKey {
			cursor = i
			if e.key == v.pendingSelect {
				v.pendingSelect = ""
			}
		}
	}

	v.rowKeys = keys
	v.table.SetRows(rows, styles)
	v.table.SetCursor(cursor)
}

// rowEntry is one table row before sorting.
type rowEntry struct {
	key string
	res k8s.Resource
	row []string
}

// sortEntries orders rows by column col (-1: by key, i.e. namespace then
// name). byAge compares creation times instead of the rendered "5m" text.
// Ties always fall back to the key, so the order is stable and doesn't
// flicker when the informer delivers updates.
func sortEntries(entries []rowEntry, col int, byAge, desc bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		var less, greater bool
		switch {
		case byAge: // youngest first, like `ls -t`
			less, greater = a.res.Created.After(b.res.Created), b.res.Created.After(a.res.Created)
		case col >= 0:
			less, greater = naturalLess(a.row[col], b.row[col]), naturalLess(b.row[col], a.row[col])
		default:
			less, greater = a.key < b.key, b.key < a.key
		}
		if !less && !greater {
			return a.key < b.key
		}
		if desc {
			return greater
		}
		return less
	})
}

func sortArrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
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
	b = append(b, k.YAML, k.Filter, k.Sort, k.Reverse)
	if v.filter.Value() != "" {
		b = append(b, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")))
	}
	return b
}
