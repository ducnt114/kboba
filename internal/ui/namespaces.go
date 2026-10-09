package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/kboba/internal/k8s"
)

// allNamespaces is the namespace value meaning "every namespace". It matches
// metav1.NamespaceAll.
const allNamespaces = ""

// requestTimeout bounds one-shot API calls so a dead cluster can't leave a
// command hanging forever.
const requestTimeout = 15 * time.Second

type namespacesLoadedMsg struct {
	names []string
	err   error
}

// namespaceSelectedMsg asks the root model to switch namespace.
type namespaceSelectedMsg struct{ namespace string }

type namespaceItem struct {
	name    string // allNamespaces for the "all" entry
	current bool
}

func (i namespaceItem) FilterValue() string { return i.label() }
func (i namespaceItem) Description() string { return "" }

func (i namespaceItem) Title() string {
	if i.current {
		return "* " + i.label()
	}
	return "  " + i.label()
}

func (i namespaceItem) label() string {
	if i.name == allNamespaces {
		return "(all namespaces)"
	}
	return i.name
}

// namespacesView lists the namespaces of the current context, plus an
// "all namespaces" entry.
type namespacesView struct {
	list list.Model
	// current is the namespace the app is showing; set by the root model.
	current string
}

func newNamespacesView() namespacesView {
	l := newList("Namespaces")
	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	d.SetSpacing(0)
	l.SetDelegate(d)
	return namespacesView{list: l}
}

func loadNamespaces(c k8s.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		names, err := c.ListNamespaces(ctx)
		return namespacesLoadedMsg{names: names, err: err}
	}
}

func (v namespacesView) Update(msg tea.Msg) (namespacesView, tea.Cmd) {
	switch msg := msg.(type) {
	case namespacesLoadedMsg:
		names := msg.names
		var errCmd tea.Cmd
		if msg.err != nil {
			// e.g. RBAC forbids listing namespaces: still offer "all" and
			// the current one; ":ns <name>" works for anything else.
			errCmd = reportErr(msg.err)
			if v.current != allNamespaces {
				names = []string{v.current}
			}
		}
		names = append([]string{allNamespaces}, names...)

		items := make([]list.Item, len(names))
		selected := 0
		for i, n := range names {
			items[i] = namespaceItem{name: n, current: n == v.current}
			if n == v.current {
				selected = i
			}
		}
		cmd := v.list.SetItems(items)
		v.list.Select(selected)
		return v, tea.Batch(cmd, errCmd)
	}

	var (
		cmd      tea.Cmd
		selected bool
	)
	v.list, cmd, selected = updateList(v.list, msg)
	if selected {
		name := v.list.SelectedItem().(namespaceItem).name
		return v, func() tea.Msg { return namespaceSelectedMsg{namespace: name} }
	}
	return v, cmd
}

func (v namespacesView) View() string { return v.list.View() }

func (v *namespacesView) SetSize(w, h int) { v.list.SetSize(w, h) }

func (v namespacesView) capturingInput() bool { return v.list.FilterState() == list.Filtering }

func (v namespacesView) keys() []key.Binding { return listHelp() }
