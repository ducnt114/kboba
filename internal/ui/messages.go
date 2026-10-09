package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// requestTimeout bounds one-shot API calls so a dead cluster can't leave a
// command hanging forever.
const requestTimeout = 15 * time.Second

// Messages shared between the root model and the views. View-specific
// messages live next to the view that consumes them.

// statusMsg sets the text in the status bar.
type statusMsg struct {
	text  string
	isErr bool
}

// backMsg asks the root model to leave the current view.
type backMsg struct{}

func reportErr(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: err.Error(), isErr: true} }
}

func reportInfo(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text} }
}

func goBack() tea.Msg { return backMsg{} }
