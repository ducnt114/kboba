// Package ui implements kboba's terminal UI with Bubble Tea.
//
// The root Model only coordinates: it owns the shared chrome (header,
// command bar, status bar, help bar), routes messages, and switches between
// views. Each screen is its own sub-model with its own Update and View.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ducnt114/kboba/internal/k8s"
)

// ClientFactory creates a client bound to a kubeconfig context. It is
// injected so tests can use a fake client.
type ClientFactory func(contextName string) (k8s.Client, error)

// Options are the startup settings, usually from command-line flags.
type Options struct {
	Context   string // empty: kubeconfig current-context
	Namespace string // empty: the context's default namespace
}

type viewID int

const (
	viewPods viewID = iota
	viewContexts
	viewNamespaces
	viewLogs
	viewDescribe
)

// clientReadyMsg is sent once a client for a (new) context has been created.
type clientReadyMsg struct {
	client   k8s.Client
	context  k8s.ContextInfo
	contexts []k8s.ContextInfo
	err      error
}

// Model is the root Bubble Tea model.
type Model struct {
	newClient ClientFactory
	opts      Options

	// Cluster state. Switching context or namespace only changes these
	// fields; nothing is written back to the kubeconfig.
	client    k8s.Client
	context   string
	namespace string

	active     viewID
	contexts   contextsView
	namespaces namespacesView
	pods       podsView
	logs       logsView
	describe   describeView

	commandMode bool
	command     textinput.Model
	help        help.Model

	status      string
	statusIsErr bool

	width, height int
	bodyHeight    int
}

// New returns the root model.
func New(newClient ClientFactory, opts Options) Model {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.Placeholder = "ctx | ns | pods | quit"

	return Model{
		newClient:  newClient,
		opts:       opts,
		active:     viewPods,
		contexts:   newContextsView(),
		namespaces: newNamespacesView(),
		pods:       newPodsView(),
		logs:       newLogsView(),
		describe:   newDescribeView(),
		command:    ti,
		help:       help.New(),
		status:     "connecting…",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(connect(m.newClient, m.opts.Context), ageTick())
}

// connect builds a client for contextName off the UI goroutine.
func connect(newClient ClientFactory, contextName string) tea.Cmd {
	return func() tea.Msg {
		c, err := newClient(contextName)
		if err != nil {
			return clientReadyMsg{err: err}
		}
		ctxs, err := c.ListContexts()
		if err != nil {
			return clientReadyMsg{err: err}
		}
		for _, ci := range ctxs {
			if ci.Current {
				return clientReadyMsg{client: c, context: ci, contexts: ctxs}
			}
		}
		// Unknown context: keep the contexts so the user can pick a valid one.
		err = fmt.Errorf("context %q not found in kubeconfig", contextName)
		return clientReadyMsg{contexts: ctxs, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		m.command.Width = msg.Width - 2
		m.layout()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case statusMsg:
		m.status, m.statusIsErr = msg.text, msg.isErr
		return m, nil

	case clientReadyMsg:
		return m.handleClientReady(msg)

	case contextSelectedMsg:
		return m.switchContext(msg.name)

	case namespaceSelectedMsg:
		return m.switchNamespace(msg.namespace)

	case openLogsMsg:
		cmd := m.logs.open(m.client, msg.pod)
		m.setActive(viewLogs)
		return m, cmd

	case openDescribeMsg:
		cmd := m.describe.open(m.client, msg.pod)
		m.setActive(viewDescribe)
		return m, cmd

	case backMsg:
		if m.client != nil {
			m.setActive(viewPods)
		}
		return m, nil

	case contextsLoadedMsg:
		var cmd tea.Cmd
		m.contexts, cmd = m.contexts.Update(msg)
		return m, cmd

	case namespacesLoadedMsg:
		var cmd tea.Cmd
		m.namespaces, cmd = m.namespaces.Update(msg)
		return m, cmd

	// The pod watch keeps running whatever view is active.
	case podWatchStartedMsg, podEventsMsg, podWatchClosedMsg, ageTickMsg:
		var cmd tea.Cmd
		m.pods, cmd = m.pods.Update(msg)
		return m, cmd

	case logStreamStartedMsg, logLinesMsg, logStreamEndedMsg:
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(msg)
		return m, cmd

	case describeLoadedMsg:
		var cmd tea.Cmd
		m.describe, cmd = m.describe.Update(msg)
		return m, cmd
	}

	// Anything else (e.g. internal messages of bubbles components) goes to
	// the active view.
	return m.updateActive(msg)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	if m.commandMode {
		switch msg.Type {
		case tea.KeyEsc:
			m.exitCommandMode()
			return m, nil
		case tea.KeyEnter:
			input := m.command.Value()
			m.exitCommandMode()
			return m.runCommand(input)
		}
		var cmd tea.Cmd
		m.command, cmd = m.command.Update(msg)
		return m, cmd
	}

	// While a view's text input (e.g. a filter) has focus, every key is
	// typed into it rather than treated as a shortcut.
	if !m.activeCapturingInput() {
		switch {
		case key.Matches(msg, globalKeyMap.Quit):
			return m, tea.Quit
		case key.Matches(msg, globalKeyMap.Command):
			m.commandMode = true
			m.command.SetValue("")
			return m, m.command.Focus()
		case key.Matches(msg, globalKeyMap.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.layout()
			return m, nil
		}
	}
	return m.updateActive(msg)
}

func (m *Model) exitCommandMode() {
	m.commandMode = false
	m.command.Blur()
}

// runCommand executes a k9s-style ":" command.
func (m Model) runCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return m, nil
	}
	arg := ""
	if len(fields) > 1 {
		arg = fields[1]
	}

	switch fields[0] {
	case "ctx", "context", "contexts":
		if arg != "" {
			return m.switchContext(arg)
		}
		return m.showContexts()
	case "ns", "namespace", "namespaces":
		if arg != "" {
			if arg == "all" || arg == "-A" {
				arg = allNamespaces
			}
			return m.switchNamespace(arg)
		}
		return m.showNamespaces()
	case "pods", "pod", "po":
		if m.client != nil {
			m.setActive(viewPods)
		}
		return m, nil
	case "q", "quit":
		return m, tea.Quit
	}
	return m, reportErr(fmt.Errorf("unknown command %q", fields[0]))
}

func (m Model) handleClientReady(msg clientReadyMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status, m.statusIsErr = msg.err.Error(), true
		if msg.contexts == nil {
			return m, nil
		}
		// Let the user choose another context.
		m.setActive(viewContexts)
		var cmd tea.Cmd
		m.contexts, cmd = m.contexts.Update(contextsLoadedMsg{contexts: msg.contexts})
		return m, cmd
	}

	firstConnect := m.client == nil
	m.client = msg.client
	m.context = msg.context.Name

	switch {
	case firstConnect && m.opts.Namespace != "":
		m.namespace = m.opts.Namespace
	case msg.context.Namespace != "":
		m.namespace = msg.context.Namespace
	default:
		m.namespace = "default"
	}

	m.status, m.statusIsErr = fmt.Sprintf("context %q", m.context), false
	var cmd tea.Cmd
	m.contexts, cmd = m.contexts.Update(contextsLoadedMsg{contexts: msg.contexts})
	// start stops the previous context's informer before creating a new one.
	watchCmd := m.pods.start(m.client, m.namespace)
	m.setActive(viewPods)
	return m, tea.Batch(cmd, watchCmd)
}

func (m Model) switchContext(name string) (tea.Model, tea.Cmd) {
	m.status, m.statusIsErr = fmt.Sprintf("switching to context %q…", name), false
	return m, connect(m.newClient, name)
}

func (m Model) switchNamespace(ns string) (tea.Model, tea.Cmd) {
	m.namespace = ns
	m.status, m.statusIsErr = "", false
	if m.client == nil {
		return m, nil
	}
	cmd := m.pods.start(m.client, ns)
	m.setActive(viewPods)
	return m, cmd
}

func (m Model) showNamespaces() (tea.Model, tea.Cmd) {
	m.setActive(viewNamespaces)
	m.namespaces.current = m.namespace
	if m.client == nil {
		return m, nil
	}
	return m, loadNamespaces(m.client)
}

func (m Model) showContexts() (tea.Model, tea.Cmd) {
	m.setActive(viewContexts)
	if m.client == nil {
		return m, nil
	}
	return m, loadContexts(m.client)
}

func (m *Model) setActive(v viewID) {
	// Leaving the logs view must cancel the stream so no goroutine or HTTP
	// connection is leaked.
	if m.active == viewLogs && v != viewLogs {
		m.logs.stop()
	}
	m.active = v
	m.layout()
}

// updateActive forwards a message to the active view.
func (m Model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.active {
	case viewContexts:
		m.contexts, cmd = m.contexts.Update(msg)
	case viewNamespaces:
		m.namespaces, cmd = m.namespaces.Update(msg)
	case viewPods:
		m.pods, cmd = m.pods.Update(msg)
	case viewLogs:
		m.logs, cmd = m.logs.Update(msg)
	case viewDescribe:
		m.describe, cmd = m.describe.Update(msg)
	}
	return m, cmd
}

func (m Model) activeCapturingInput() bool {
	switch m.active {
	case viewContexts:
		return m.contexts.capturingInput()
	case viewNamespaces:
		return m.namespaces.capturingInput()
	case viewPods:
		return m.pods.capturingInput()
	case viewLogs:
		return m.logs.capturingInput()
	case viewDescribe:
		return m.describe.capturingInput()
	}
	return false
}

func (m Model) activeKeys() helpKeys {
	switch m.active {
	case viewContexts:
		return helpKeys{view: m.contexts.keys()}
	case viewNamespaces:
		return helpKeys{view: m.namespaces.keys()}
	case viewPods:
		return helpKeys{view: m.pods.keys()}
	case viewLogs:
		return helpKeys{view: m.logs.keys()}
	case viewDescribe:
		return helpKeys{view: m.describe.keys()}
	}
	return helpKeys{}
}

// layout recomputes the body size and propagates it to every view.
func (m *Model) layout() {
	if m.width == 0 {
		return
	}
	// header + status line + help bar
	chrome := 2 + lipgloss.Height(m.help.View(m.activeKeys()))
	h := max(m.height-chrome, 1)
	m.bodyHeight = h
	m.contexts.SetSize(m.width, h)
	m.namespaces.SetSize(m.width, h)
	m.pods.SetSize(m.width, h)
	m.logs.SetSize(m.width, h)
	m.describe.SetSize(m.width, h)
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}

	var body string
	switch m.active {
	case viewContexts:
		body = m.contexts.View()
	case viewNamespaces:
		body = m.namespaces.View()
	case viewPods:
		body = m.pods.View()
	case viewLogs:
		body = m.logs.View()
	case viewDescribe:
		body = m.describe.View()
	}

	// Pin the body height so the status and help bars stay at the bottom.
	body = lipgloss.NewStyle().Height(m.bodyHeight).MaxHeight(m.bodyHeight).Render(body)

	return lipgloss.JoinVertical(lipgloss.Left,
		m.headerView(),
		body,
		m.statusView(),
		m.help.View(m.activeKeys()),
	)
}

func (m Model) headerView() string {
	ns := m.namespace
	if ns == "" {
		ns = "(all)"
	}
	ctx := m.context
	if ctx == "" {
		ctx = "-"
	}
	info := fmt.Sprintf("ctx: %s  ns: %s", ctx, ns)
	return lipgloss.NewStyle().MaxWidth(m.width).Render(
		headerStyle.Render("kboba") + headerInfoStyle.Render(info),
	)
}

func (m Model) statusView() string {
	if m.commandMode {
		return m.command.View()
	}
	style := statusStyle
	if m.statusIsErr {
		style = statusErrorStyle
	}
	return style.MaxWidth(m.width).Render(m.status)
}
