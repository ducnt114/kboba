package ui

import (
	"context"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
)

// detailLoadedMsg carries the text of a detail view. id says which request
// it answers, so a late reply for something we already left is dropped.
type detailLoadedMsg struct {
	id   string
	text string
	err  error
}

var detailRefreshKey = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))

// detailLoader fetches the text shown by a detailView. It runs inside a
// tea.Cmd, off the UI goroutine.
type detailLoader func(ctx context.Context) (string, error)

// detailView shows a read-only text snapshot: a pod description or an
// object's YAML. Both are "fetch some text, show it in a scrollable
// viewport, allow refresh", so they share this one view.
type detailView struct {
	viewport viewport.Model
	title    string
	id       string
	wrap     bool // wrap long lines (describe) or scroll horizontally (YAML)
	load     detailLoader
	text     string
	loading  bool
}

func newDetailView() detailView {
	vp := viewport.New(0, 0)
	vp.SetHorizontalStep(4)
	return detailView{viewport: vp}
}

func (v *detailView) open(title, id string, wrap bool, load detailLoader) tea.Cmd {
	v.title, v.id, v.wrap, v.load = title, id, wrap, load
	v.text = ""
	v.viewport.SetContent("")
	v.viewport.GotoTop()
	v.viewport.SetXOffset(0)
	return v.refresh()
}

func (v *detailView) refresh() tea.Cmd {
	v.loading = true
	id, load := v.id, v.load
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		text, err := load(ctx)
		return detailLoadedMsg{id: id, text: text, err: err}
	}
}

// openDescribe shows `kubectl describe`-like output for a pod.
func (v *detailView) openDescribe(c k8s.Client, pod k8s.PodInfo) tea.Cmd {
	return v.open("Describe "+pod.Key(), "describe:"+pod.Key(), true,
		func(ctx context.Context) (string, error) {
			return c.DescribePod(ctx, pod.Namespace, pod.Name)
		})
}

// openYAML shows an object's YAML, syntax-highlighted.
func (v *detailView) openYAML(c k8s.Client, rt *k8s.ResourceType, r k8s.Resource) tea.Cmd {
	name := r.Name
	if r.Namespace != "" {
		name = r.Namespace + "/" + r.Name
	}
	return v.open("YAML "+rt.Name+" "+name, "yaml:"+rt.Name+":"+r.Key(), false,
		func(ctx context.Context) (string, error) {
			y, err := c.GetYAML(ctx, rt, r.Namespace, r.Name)
			if err != nil {
				return "", err
			}
			return highlightYAML(y), nil
		})
}

// highlightYAML colours YAML for the terminal. On any error it returns the
// input unchanged: colours are a nicety, never a reason to fail.
func highlightYAML(src string) string {
	style := "github"
	if lipgloss.HasDarkBackground() {
		style = "monokai"
	}
	var b strings.Builder
	if err := quick.Highlight(&b, src, "yaml", "terminal256", style); err != nil {
		return src
	}
	return b.String()
}

func (v detailView) Update(msg tea.Msg) (detailView, tea.Cmd) {
	switch msg := msg.(type) {
	case detailLoadedMsg:
		if msg.id != v.id {
			return v, nil // a request for something we already left
		}
		v.loading = false
		if msg.err != nil {
			// e.g. the object was deleted meanwhile.
			return v, reportErr(msg.err)
		}
		v.text = msg.text
		v.render()
		return v, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, globalKeyMap.Back):
			return v, goBack
		case key.Matches(msg, detailRefreshKey):
			return v, v.refresh()
		}
		var cmd tea.Cmd
		v.viewport, cmd = v.viewport.Update(msg)
		return v, cmd
	}
	return v, nil
}

// render (re)wraps the text for the current width; it runs again on resize.
func (v *detailView) render() {
	if !v.wrap || v.viewport.Width <= 0 {
		v.viewport.SetContent(v.text)
		return
	}
	v.viewport.SetContent(lipgloss.NewStyle().Width(v.viewport.Width).Render(v.text))
}

func (v *detailView) SetSize(w, h int) {
	v.viewport.Width = w
	v.viewport.Height = max(h-1, 1) // one line for the title
	v.render()
}

func (v detailView) View() string {
	title := titleStyle.Render(v.title)
	if v.loading {
		title += statusStyle.Render("  loading…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, v.viewport.View())
}

func (v detailView) capturingInput() bool { return false }

func (v detailView) keys() []key.Binding {
	b := []key.Binding{logsKeyMap.Up, logsKeyMap.Down}
	if !v.wrap {
		b = append(b, key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "scroll")))
	}
	return append(b, detailRefreshKey, globalKeyMap.Back)
}
