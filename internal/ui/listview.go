package ui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// newList builds a bubbles list configured for kboba: filtering on "/",
// no built-in help (the root model renders its own help bar) and no
// quit bindings (quitting is the root model's job).
func newList(title string) list.Model {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.Title = title
	l.Styles.Title = titleStyle
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.KeyMap.Quit.SetEnabled(false)
	l.KeyMap.ForceQuit.SetEnabled(false)
	return l
}

// updateList handles keys common to the list-based views. It returns
// selected=true when the user pressed enter on an item, and emits backMsg
// when esc is pressed with no filter to clear.
func updateList(l list.Model, msg tea.Msg) (_ list.Model, cmd tea.Cmd, selected bool) {
	if k, ok := msg.(tea.KeyMsg); ok && l.FilterState() != list.Filtering {
		switch {
		case key.Matches(k, listKeyMap.Select):
			return l, nil, l.SelectedItem() != nil
		case key.Matches(k, globalKeyMap.Back) && l.FilterState() == list.Unfiltered:
			return l, goBack, false
		}
	}
	l, cmd = l.Update(msg)
	return l, cmd, false
}

func listHelp() []key.Binding {
	k := listKeyMap
	return []key.Binding{k.Up, k.Down, k.Select, k.Filter}
}
