package ui

import (
	"context"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
)

type describeLoadedMsg struct {
	key  string // pod key the description belongs to
	text string
	err  error
}

var describeRefreshKey = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))

// describeView shows a snapshot of a pod's details and events.
type describeView struct {
	viewport viewport.Model
	client   k8s.Client
	pod      k8s.PodInfo
	text     string
	loading  bool
}

func newDescribeView() describeView {
	return describeView{viewport: viewport.New(0, 0)}
}

func (v *describeView) open(c k8s.Client, pod k8s.PodInfo) tea.Cmd {
	v.client = c
	v.pod = pod
	v.text = ""
	v.viewport.SetContent("")
	v.viewport.GotoTop()
	return v.load()
}

func (v *describeView) load() tea.Cmd {
	v.loading = true
	c, pod := v.client, v.pod
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		text, err := c.DescribePod(ctx, pod.Namespace, pod.Name)
		return describeLoadedMsg{key: pod.Key(), text: text, err: err}
	}
}

func (v describeView) Update(msg tea.Msg) (describeView, tea.Cmd) {
	switch msg := msg.(type) {
	case describeLoadedMsg:
		if msg.key != v.pod.Key() {
			return v, nil // a pod we already left
		}
		v.loading = false
		if msg.err != nil {
			// e.g. the pod was deleted meanwhile.
			return v, reportErr(msg.err)
		}
		v.text = msg.text
		v.render()
		return v, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, globalKeyMap.Back):
			return v, goBack
		case key.Matches(msg, describeRefreshKey):
			return v, v.load()
		}
		var cmd tea.Cmd
		v.viewport, cmd = v.viewport.Update(msg)
		return v, cmd
	}
	return v, nil
}

// render wraps the text to the current width; it runs again on resize.
func (v *describeView) render() {
	if v.viewport.Width <= 0 {
		v.viewport.SetContent(v.text)
		return
	}
	v.viewport.SetContent(lipgloss.NewStyle().Width(v.viewport.Width).Render(v.text))
}

func (v *describeView) SetSize(w, h int) {
	v.viewport.Width = w
	v.viewport.Height = max(h-1, 1) // one line for the title
	v.render()
}

func (v describeView) View() string {
	title := titleStyle.Render("Describe " + v.pod.Key())
	if v.loading {
		title += statusStyle.Render("  loading…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, v.viewport.View())
}

func (v describeView) capturingInput() bool { return false }

func (v describeView) keys() []key.Binding {
	return []key.Binding{logsKeyMap.Up, logsKeyMap.Down, describeRefreshKey, globalKeyMap.Back}
}
