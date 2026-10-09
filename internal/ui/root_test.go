package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ducnt114/kboba/internal/k8s"
)

// fakeClient is an in-memory k8s.Client for UI tests.
type fakeClient struct {
	context    string
	contexts   []k8s.ContextInfo
	namespaces []string
	nsErr      error
}

func (f *fakeClient) ListContexts() ([]k8s.ContextInfo, error) {
	out := make([]k8s.ContextInfo, len(f.contexts))
	for i, c := range f.contexts {
		c.Current = c.Name == f.context
		out[i] = c
	}
	return out, nil
}

func (f *fakeClient) ListNamespaces(context.Context) ([]string, error) {
	return f.namespaces, f.nsErr
}

func fakeFactory(contexts ...k8s.ContextInfo) ClientFactory {
	return func(name string) (k8s.Client, error) {
		if name == "" {
			name = contexts[0].Name
		}
		return &fakeClient{context: name, contexts: contexts, namespaces: []string{"default", "team-a"}}, nil
	}
}

// send runs msg through Update, then executes the returned commands and
// feeds their messages back in, recursively. Commands that don't finish
// quickly (cursor blink timers, blocking waits on channels) are dropped.
func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	return sendDepth(m, msg, 0)
}

func sendDepth(m Model, msg tea.Msg, depth int) Model {
	next, cmd := m.Update(msg)
	m = next.(Model)
	if depth >= 5 {
		return m
	}
	for _, out := range runCmd(cmd) {
		m = sendDepth(m, out, depth+1)
	}
	return m
}

// runCmd executes cmd and returns the resulting messages, expanding
// tea.Batch. A command that hasn't returned within a short timeout is
// abandoned.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return nil
	}
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

// typeCommand simulates typing ":<cmd>" followed by enter.
func typeCommand(t *testing.T, m Model, cmd string) Model {
	t.Helper()
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	for _, r := range cmd {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}

func TestNamespaceCommands(t *testing.T) {
	m := startModel(t, Options{})

	m = typeCommand(t, m, "ns")
	if m.active != viewNamespaces {
		t.Fatalf("active view = %v, want namespaces", m.active)
	}
	// "(all namespaces)" + default + team-a
	if n := len(m.namespaces.list.Items()); n != 3 {
		t.Fatalf("got %d namespace items, want 3", n)
	}

	m = typeCommand(t, m, "ns all")
	if m.namespace != allNamespaces {
		t.Fatalf("namespace = %q, want all", m.namespace)
	}
	m = typeCommand(t, m, "ns kube-system")
	if m.namespace != "kube-system" {
		t.Fatalf("namespace = %q", m.namespace)
	}
}

func TestNamespacesForbiddenFallsBack(t *testing.T) {
	m := startModel(t, Options{})
	m.client.(*fakeClient).nsErr = errors.New("namespaces is forbidden")

	m = typeCommand(t, m, "ns")
	if !m.statusIsErr {
		t.Fatal("expected error in status bar")
	}
	// "(all namespaces)" + the current namespace remain selectable.
	if n := len(m.namespaces.list.Items()); n != 2 {
		t.Fatalf("got %d items, want 2", n)
	}
}
