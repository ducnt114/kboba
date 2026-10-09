package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/kboba/internal/k8s"
)

// contextsLoadedMsg carries the kubeconfig contexts.
type contextsLoadedMsg struct {
	contexts []k8s.ContextInfo
	err      error
}

// contextSelectedMsg asks the root model to switch to another context.
type contextSelectedMsg struct{ name string }

// contextItem adapts k8s.ContextInfo to list.Item / list.DefaultItem.
type contextItem struct{ info k8s.ContextInfo }

func (i contextItem) FilterValue() string { return i.info.Name }

func (i contextItem) Title() string {
	if i.info.Current {
		return "* " + i.info.Name
	}
	return "  " + i.info.Name
}

func (i contextItem) Description() string {
	ns := i.info.Namespace
	if ns == "" {
		ns = "default"
	}
	return fmt.Sprintf("  cluster: %s  user: %s  namespace: %s", i.info.Cluster, i.info.User, ns)
}

// contextsView lists the kubeconfig contexts.
type contextsView struct {
	list list.Model
}

func newContextsView() contextsView {
	return contextsView{list: newList("Contexts")}
}

// loadContexts reads the contexts from the kubeconfig. It runs as a tea.Cmd
// because it touches the filesystem.
func loadContexts(c k8s.Client) tea.Cmd {
	return func() tea.Msg {
		ctxs, err := c.ListContexts()
		return contextsLoadedMsg{contexts: ctxs, err: err}
	}
}

func (v contextsView) Update(msg tea.Msg) (contextsView, tea.Cmd) {
	switch msg := msg.(type) {
	case contextsLoadedMsg:
		if msg.err != nil {
			return v, reportErr(msg.err)
		}
		items := make([]list.Item, len(msg.contexts))
		current := 0
		for i, c := range msg.contexts {
			items[i] = contextItem{info: c}
			if c.Current {
				current = i
			}
		}
		cmd := v.list.SetItems(items)
		v.list.Select(current)
		return v, cmd
	}

	var (
		cmd      tea.Cmd
		selected bool
	)
	v.list, cmd, selected = updateList(v.list, msg)
	if selected {
		name := v.list.SelectedItem().(contextItem).info.Name
		return v, func() tea.Msg { return contextSelectedMsg{name: name} }
	}
	return v, cmd
}

func (v contextsView) View() string { return v.list.View() }

func (v *contextsView) SetSize(w, h int) { v.list.SetSize(w, h) }

// capturingInput reports whether keystrokes should go to the view's text
// input instead of being interpreted as global shortcuts.
func (v contextsView) capturingInput() bool { return v.list.FilterState() == list.Filtering }

func (v contextsView) keys() []key.Binding { return listHelp() }
