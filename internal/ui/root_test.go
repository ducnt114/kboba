package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/kboba/internal/k8s"
)

// fakeClient is an in-memory k8s.Client for UI tests.
type fakeClient struct {
	context  string
	contexts []k8s.ContextInfo
}

func (f *fakeClient) ListContexts() ([]k8s.ContextInfo, error) {
	out := make([]k8s.ContextInfo, len(f.contexts))
	for i, c := range f.contexts {
		c.Current = c.Name == f.context
		out[i] = c
	}
	return out, nil
}

func fakeFactory(contexts ...k8s.ContextInfo) ClientFactory {
	return func(name string) (k8s.Client, error) {
		if name == "" {
			name = contexts[0].Name
		}
		return &fakeClient{context: name, contexts: contexts}, nil
	}
}

// send runs msg through Update, then executes the returned commands
// synchronously and feeds their messages back in (one level deep).
func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	for _, out := range runCmd(cmd) {
		next, _ = m.Update(out)
		m = next.(Model)
	}
	return m
}

// runCmd executes cmd once and returns the resulting messages, expanding
// tea.Batch.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func startModel(t *testing.T, opts Options) Model {
	t.Helper()
	m := New(fakeFactory(
		k8s.ContextInfo{Name: "dev", Namespace: "team-a"},
		k8s.ContextInfo{Name: "prod"},
	), opts)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	return send(t, m, m.Init()())
}

func TestStartupUsesContextNamespace(t *testing.T) {
	m := startModel(t, Options{})
	if m.context != "dev" || m.namespace != "team-a" {
		t.Fatalf("got ctx=%q ns=%q", m.context, m.namespace)
	}
}

func TestNamespaceFlagWins(t *testing.T) {
	m := startModel(t, Options{Namespace: "kube-system"})
	if m.namespace != "kube-system" {
		t.Fatalf("got ns=%q", m.namespace)
	}
}

func TestSwitchContext(t *testing.T) {
	m := startModel(t, Options{})
	m = send(t, m, contextSelectedMsg{name: "prod"})
	if m.context != "prod" || m.namespace != "default" {
		t.Fatalf("got ctx=%q ns=%q", m.context, m.namespace)
	}
}

func TestSwitchToUnknownContextKeepsCurrent(t *testing.T) {
	m := startModel(t, Options{})
	m = send(t, m, contextSelectedMsg{name: "nope"})
	if m.context != "dev" {
		t.Fatalf("context changed to %q", m.context)
	}
	if !m.statusIsErr {
		t.Fatal("expected an error in the status bar")
	}
}
