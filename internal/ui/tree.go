package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
)

type (
	openTreeMsg struct {
		rt       *k8s.ResourceType
		resource k8s.Resource
	}
	treeLoadedMsg struct {
		id   string
		root *k8s.TreeNode
		err  error
	}
)

var treeKeyMap = struct {
	Tree, Refresh key.Binding
}{
	Tree:    key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "tree")),
	Refresh: detailRefreshKey,
}

// treeRow is one line of a flattened tree: the drawing in front of the
// node ("│  ├─ ") and the node itself.
type treeRow struct {
	prefix string
	node   *k8s.TreeNode
}

// flattenTree turns a tree into display rows, drawing the branches the way
// `tree` does.
func flattenTree(root *k8s.TreeNode) []treeRow {
	rows := []treeRow{{node: root}}
	var walk func(n *k8s.TreeNode, indent string)
	walk = func(n *k8s.TreeNode, indent string) {
		for i, c := range n.Children {
			branch, next := "├─ ", "│  "
			if i == len(n.Children)-1 {
				branch, next = "└─ ", "   "
			}
			rows = append(rows, treeRow{prefix: indent + branch, node: c})
			walk(c, indent+next)
		}
	}
	walk(root, "")
	return rows
}

// treeView shows how objects relate: a Deployment's ReplicaSets and their
// Pods, a Service's Pods, a Pod's owners. It is a snapshot (r refreshes).
type treeView struct {
	table   tableModel
	client  k8s.Client
	rt      *k8s.ResourceType
	root    k8s.Resource
	id      string
	rows    []treeRow
	loading bool
	width   int
}

func newTreeView() treeView { return treeView{table: newTableModel()} }

func (v *treeView) open(c k8s.Client, rt *k8s.ResourceType, r k8s.Resource) tea.Cmd {
	v.client, v.rt, v.root = c, rt, r
	v.id = rt.Name + ":" + r.Key()
	v.rows = nil
	v.table.SetRows(nil, nil)
	v.table.SetCursor(0)
	return v.load()
}

func (v *treeView) load() tea.Cmd {
	v.loading = true
	c, rt, r, id := v.client, v.rt, v.root, v.id
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		root, err := buildTree(ctx, c, rt, r)
		return treeLoadedMsg{id: id, root: root, err: err}
	}
}

// buildTree fetches what's needed for rt's tree. Ownership is followed by
// UID (k8s.OwnerTree); a Service doesn't own its pods, it selects them.
func buildTree(ctx context.Context, c k8s.Client, rt *k8s.ResourceType, r k8s.Resource) (*k8s.TreeNode, error) {
	list := func(t *k8s.ResourceType, selector string) ([]k8s.Resource, error) {
		return c.ListResources(ctx, t, r.Namespace, selector)
	}
	switch rt {
	case k8s.Deployments:
		// All ReplicaSets of the namespace, not just those matching the
		// selector: ownership (UID) is the source of truth, labels are only
		// a convention. OwnerTree keeps the ones this Deployment owns.
		rss, err := list(k8s.ReplicaSets, "")
		if err != nil {
			return nil, err
		}
		pods, err := list(k8s.Pods, r.Selector)
		if err != nil {
			return nil, err
		}
		return k8s.OwnerTree(rt, r, k8s.Level{Type: k8s.ReplicaSets, Items: rss}, k8s.Level{Type: k8s.Pods, Items: pods}), nil

	case k8s.Services:
		root := &k8s.TreeNode{Type: rt, Resource: r}
		if r.Selector == "" {
			return root, nil // selects nothing (e.g. ExternalName)
		}
		pods, err := list(k8s.Pods, r.Selector)
		if err != nil {
			return nil, err
		}
		for _, p := range pods {
			root.Children = append(root.Children, &k8s.TreeNode{Type: k8s.Pods, Resource: p})
		}
		return root, nil

	case k8s.Pods:
		return podAncestors(ctx, c, r)

	default:
		if !rt.Namespaced {
			return &k8s.TreeNode{Type: rt, Resource: r}, nil
		}
		// ReplicaSets, StatefulSets, DaemonSets, Jobs, CRDs…: pods they own.
		pods, err := list(k8s.Pods, r.Selector)
		if err != nil {
			return nil, err
		}
		return k8s.OwnerTree(rt, r, k8s.Level{Type: k8s.Pods, Items: pods}), nil
	}
}

// podAncestors walks a pod's ownerReferences upwards (Pod → ReplicaSet →
// Deployment) and returns that chain as a tree rooted at the top owner.
func podAncestors(ctx context.Context, c k8s.Client, pod k8s.Resource) (*k8s.TreeNode, error) {
	chain := []*k8s.TreeNode{{Type: k8s.Pods, Resource: pod}}
	owners := map[string]*k8s.ResourceType{"ReplicaSet": k8s.ReplicaSets, "Deployment": k8s.Deployments}
	cur := pod
	for len(cur.Owners) > 0 {
		o := cur.Owners[0]
		t, known := owners[o.Kind]
		if !known {
			break // e.g. a StatefulSet: not a built-in view, stop here
		}
		items, err := c.ListResources(ctx, t, pod.Namespace, "")
		if err != nil {
			return nil, err
		}
		var found *k8s.Resource
		for i := range items {
			if items[i].UID == o.UID {
				found = &items[i]
			}
		}
		if found == nil {
			break // owner already deleted
		}
		chain = append(chain, &k8s.TreeNode{Type: t, Resource: *found})
		cur = *found
	}
	for i := len(chain) - 1; i > 0; i-- {
		chain[i].Children = []*k8s.TreeNode{chain[i-1]}
	}
	return chain[len(chain)-1], nil
}

// nodeStatus is the STATUS column of a tree row.
func nodeStatus(n *k8s.TreeNode) string {
	r := n.Resource
	switch n.Type {
	case k8s.Pods:
		if r.Pod != nil {
			return r.Pod.Status
		}
	case k8s.Deployments:
		return "ready " + cell(r, 1)
	case k8s.ReplicaSets:
		return "ready " + cell(r, 3) + "/" + cell(r, 1)
	case k8s.Services:
		return cell(r, 1)
	}
	return ""
}

func (v treeView) Update(msg tea.Msg) (treeView, tea.Cmd) {
	switch msg := msg.(type) {
	case treeLoadedMsg:
		if msg.id != v.id {
			return v, nil
		}
		v.loading = false
		if msg.err != nil {
			return v, reportErr(msg.err)
		}
		v.setTree(msg.root)
		return v, nil

	case tea.KeyMsg:
		n, ok := v.selected()
		switch {
		case key.Matches(msg, globalKeyMap.Back):
			return v, goBack
		case key.Matches(msg, treeKeyMap.Refresh):
			return v, v.load()
		case !ok:
		case key.Matches(msg, resourceKeyMap.Logs) && n.Type == k8s.Pods && n.Resource.Pod != nil:
			p := *n.Resource.Pod
			return v, func() tea.Msg { return openLogsMsg{pod: p} }
		case key.Matches(msg, resourceKeyMap.Describe):
			return v, func() tea.Msg { return openDescribeMsg{rt: n.Type, resource: n.Resource} }
		case key.Matches(msg, resourceKeyMap.YAML):
			return v, func() tea.Msg { return openYAMLMsg{rt: n.Type, resource: n.Resource} }
		case key.Matches(msg, resourceKeyMap.AllLogs) && n.Resource.Selector != "":
			title := n.Type.Name + "/" + n.Resource.Name
			return v, func() tea.Msg {
				return openMultiLogsMsg{title: title, namespace: n.Resource.Namespace, selector: n.Resource.Selector}
			}
		}
		var cmd tea.Cmd
		v.table, cmd = v.table.Update(msg)
		return v, cmd
	}
	return v, nil
}

func (v *treeView) setTree(root *k8s.TreeNode) {
	v.rows = flattenTree(root)
	now := time.Now()
	rows := make([][]string, len(v.rows))
	styles := make([]lipgloss.Style, len(v.rows))
	for i, tr := range v.rows {
		r := tr.node.Resource
		name := tr.prefix + tr.node.Type.Title + "/" + r.Name
		rows[i] = []string{name, nodeStatus(tr.node), formatAge(now.Sub(r.Created))}
		styles[i] = healthStyles[rowHealth(tr.node.Type, r)]
	}
	v.table.SetRows(rows, styles)
}

func (v treeView) selected() (*k8s.TreeNode, bool) {
	i := v.table.Cursor()
	if i < 0 || i >= len(v.rows) {
		return nil, false
	}
	return v.rows[i].node, true
}

func (v *treeView) SetSize(w, h int) {
	v.width = w
	v.table.SetWidth(w)
	v.table.SetHeight(max(h-1, 1))
	v.table.SetColumns([]tableColumn{
		{Title: "NAME", Width: max(w-(24+2)-(7+2)-2-2, 20)},
		{Title: "STATUS", Width: 24},
		{Title: "AGE", Width: 7},
	})
}

func (v treeView) View() string {
	title := titleStyle.Render(fmt.Sprintf("Tree %s/%s", v.rt.Name, v.root.Name))
	if v.loading {
		title += statusStyle.Render("  loading…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, v.table.View())
}

func (v treeView) capturingInput() bool { return false }

func (v treeView) keys() []key.Binding {
	k := resourceKeyMap
	b := []key.Binding{k.Up, k.Down}
	if n, ok := v.selected(); ok {
		if n.Type == k8s.Pods {
			b = append(b, k.Logs)
		}
		b = append(b, k.Describe)
		if n.Resource.Selector != "" {
			b = append(b, k.AllLogs)
		}
	}
	return append(b, k.YAML, treeKeyMap.Refresh, globalKeyMap.Back)
}
