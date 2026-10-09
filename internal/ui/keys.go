package ui

import "github.com/charmbracelet/bubbles/key"

// globalKeys are available in every view (unless a text input has focus).
type globalKeys struct {
	Command key.Binding
	Help    key.Binding
	Back    key.Binding
	Quit    key.Binding
}

var globalKeyMap = globalKeys{
	Command: key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

// listKeys are shared by the list-based views (contexts, namespaces).
type listKeys struct {
	Up     key.Binding
	Down   key.Binding
	Select key.Binding
	Filter key.Binding
}

var listKeyMap = listKeys{
	Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
	Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
}

// helpKeys combines a view's own bindings with the global ones so it can be
// rendered by bubbles/help. It implements help.KeyMap.
type helpKeys struct {
	view []key.Binding
}

func (h helpKeys) ShortHelp() []key.Binding {
	g := globalKeyMap
	return append(append([]key.Binding{}, h.view...), g.Command, g.Help, g.Quit)
}

func (h helpKeys) FullHelp() [][]key.Binding {
	g := globalKeyMap
	return [][]key.Binding{
		h.view,
		{g.Command, g.Back, g.Help, g.Quit},
		{
			key.NewBinding(key.WithKeys(":ctx"), key.WithHelp(":ctx [name]", "contexts")),
			key.NewBinding(key.WithKeys(":ns"), key.WithHelp(":ns [name]", "namespaces")),
			key.NewBinding(key.WithKeys(":pods"), key.WithHelp(":pods", "pods")),
		},
	}
}
