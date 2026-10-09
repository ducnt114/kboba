package ui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"

	"github.com/ducnt114/kboba/internal/k8s"
)

// fakeClient is an in-memory k8s.Client for UI tests.
type fakeClient struct {
	context    string
	contexts   []k8s.ContextInfo
	namespaces []string
	nsErr      error
	pods       []k8s.PodInfo
	watches    []*fakeWatch
	streams    []*fakeStream
}

// fakeStream records a StreamLogs call; ctx tells whether it was cancelled.
type fakeStream struct {
	container string
	ctx       context.Context
}

// fakeWatch is a pod watch whose events are queued up front (initial pods
// followed by PodsSynced), like an informer's initial list.
type fakeWatch struct {
	namespace string
	stopped   bool
	*k8s.PodWatch
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

func (f *fakeClient) ListPods(_ context.Context, ns string) ([]k8s.PodInfo, error) {
	var out []k8s.PodInfo
	for _, p := range f.pods {
		if ns == "" || p.Namespace == ns {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeClient) GetPod(context.Context, string, string) (*corev1.Pod, error) {
	return &corev1.Pod{}, nil
}

func (f *fakeClient) WatchPods(ns string) (*k8s.PodWatch, error) {
	pods, _ := f.ListPods(context.Background(), ns)
	ch := make(chan k8s.PodEvent, len(pods)+1)
	for _, p := range pods {
		ch <- k8s.PodEvent{Type: k8s.PodUpserted, Pod: p}
	}
	ch <- k8s.PodEvent{Type: k8s.PodsSynced}

	w := &fakeWatch{namespace: ns}
	var once sync.Once
	w.PodWatch = &k8s.PodWatch{
		Events: ch,
		Stop:   func() { once.Do(func() { w.stopped = true; close(ch) }) },
	}
	f.watches = append(f.watches, w)
	return w.PodWatch, nil
}

// StreamLogs emits two lines, then stays open (like a follow) until ctx
// is cancelled.
func (f *fakeClient) StreamLogs(ctx context.Context, _, _, container string) (<-chan string, <-chan error, error) {
	f.streams = append(f.streams, &fakeStream{container: container, ctx: ctx})
	lines := make(chan string, 2)
	lines <- container + " line 1"
	lines <- container + " line 2"
	go func() {
		<-ctx.Done()
		close(lines)
	}()
	return lines, make(chan error), nil
}

func fakeFactory(contexts ...k8s.ContextInfo) ClientFactory {
	return func(name string) (k8s.Client, error) {
		if name == "" {
			name = contexts[0].Name
		}
		return &fakeClient{
			context:    name,
			contexts:   contexts,
			namespaces: []string{"default", "team-a"},
			pods: []k8s.PodInfo{
				{Namespace: "team-a", Name: "api", Containers: []string{"app", "sidecar"}, DefaultContainer: "app"},
				{Namespace: "team-a", Name: "worker"},
				{Namespace: "default", Name: "web"},
			},
		}, nil
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
	for _, msg := range runCmd(m.Init()) {
		m = send(t, m, msg)
	}
	return m
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

func TestPodsWatchLifecycle(t *testing.T) {
	m := startModel(t, Options{})
	fc := m.client.(*fakeClient)

	if m.active != viewPods {
		t.Fatalf("active view = %v, want pods", m.active)
	}
	if len(fc.watches) != 1 || fc.watches[0].namespace != "team-a" {
		t.Fatalf("watches = %+v", fc.watches)
	}
	if got := len(m.pods.rowKeys); got != 2 {
		t.Fatalf("pods table has %d rows, want 2", got)
	}
	oldGen := m.pods.gen

	// Switching namespace must stop the old informer before starting a new one.
	m = typeCommand(t, m, "ns all")
	if !fc.watches[0].stopped {
		t.Fatal("old watch was not stopped")
	}
	if len(fc.watches) != 2 || fc.watches[1].namespace != "" {
		t.Fatalf("expected a new all-namespaces watch, got %+v", fc.watches)
	}
	if got := len(m.pods.rowKeys); got != 3 {
		t.Fatalf("pods table has %d rows, want 3", got)
	}
	if cols := m.pods.table.Columns(); cols[0].Title != "NAMESPACE" {
		t.Fatalf("first column = %q, want NAMESPACE", cols[0].Title)
	}

	// A late event from the old watch must be ignored.
	m = send(t, m, podEventsMsg{gen: oldGen, events: []k8s.PodEvent{
		{Type: k8s.PodUpserted, Pod: k8s.PodInfo{Namespace: "team-a", Name: "ghost"}},
	}})
	if got := len(m.pods.rowKeys); got != 3 {
		t.Fatalf("stale event changed the table: %d rows", got)
	}
}

func TestPodsWatchStoppedOnContextSwitch(t *testing.T) {
	m := startModel(t, Options{})
	old := m.client.(*fakeClient)

	m = send(t, m, contextSelectedMsg{name: "prod"})
	if !old.watches[0].stopped {
		t.Fatal("watch of the previous context was not stopped")
	}
	if m.pods.namespace != "default" {
		t.Fatalf("pods namespace = %q", m.pods.namespace)
	}
}

func TestPodsFilter(t *testing.T) {
	m := startModel(t, Options{})
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.pods.capturingInput() {
		t.Fatal("filter should have focus")
	}
	// "q" must be typed into the filter, not quit the app.
	for _, r := range "wor" {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.pods.rowKeys) != 1 || m.pods.rowKeys[0] != "team-a/worker" {
		t.Fatalf("filtered rows = %v", m.pods.rowKeys)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.pods.rowKeys) != 2 {
		t.Fatalf("esc should clear the filter, rows = %v", m.pods.rowKeys)
	}
}

func press(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestLogsLifecycle(t *testing.T) {
	m := startModel(t, Options{})
	fc := m.client.(*fakeClient)

	// The cursor starts on the first row: team-a/api.
	m = send(t, m, press("enter"))
	if m.active != viewLogs {
		t.Fatalf("active view = %v, want logs", m.active)
	}
	if len(fc.streams) != 1 || fc.streams[0].container != "app" {
		t.Fatalf("streams = %+v", fc.streams)
	}
	if got := m.logs.buf.String(); got != "app line 1\napp line 2" {
		t.Fatalf("buffer = %q", got)
	}
	if !m.logs.follow {
		t.Fatal("follow should start on")
	}

	m = send(t, m, press("f"))
	if m.logs.follow {
		t.Fatal("f should turn follow off")
	}

	// Switching container cancels the first stream and starts a new one.
	m = send(t, m, press("c"))
	if fc.streams[0].ctx.Err() == nil {
		t.Fatal("first stream was not cancelled")
	}
	if len(fc.streams) != 2 || fc.streams[1].container != "sidecar" {
		t.Fatalf("streams = %+v", fc.streams)
	}
	if got := m.logs.buf.String(); got != "sidecar line 1\nsidecar line 2" {
		t.Fatalf("buffer = %q", got)
	}

	// Leaving the view cancels the stream.
	m = send(t, m, press("esc"))
	if m.active != viewPods {
		t.Fatalf("active view = %v, want pods", m.active)
	}
	if fc.streams[1].ctx.Err() == nil {
		t.Fatal("stream not cancelled when leaving logs view")
	}
}

func TestLogsCancelledOnNamespaceSwitch(t *testing.T) {
	m := startModel(t, Options{})
	fc := m.client.(*fakeClient)

	m = send(t, m, press("enter"))
	m = typeCommand(t, m, "ns default")
	if m.active != viewPods {
		t.Fatalf("active view = %v", m.active)
	}
	if fc.streams[0].ctx.Err() == nil {
		t.Fatal("stream not cancelled")
	}
}
