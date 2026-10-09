package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7D79F6"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#777777"}
	colorError  = lipgloss.AdaptiveColor{Light: "#D7263D", Dark: "#FF5F87"}

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorAccent).
			Padding(0, 1)
	headerInfoStyle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	statusStyle      = lipgloss.NewStyle().Foreground(colorMuted)
	statusErrorStyle = lipgloss.NewStyle().Foreground(colorError).Bold(true)

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
)
