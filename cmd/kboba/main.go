// Command kboba is a small, read-only, k9s-like terminal UI for Kubernetes.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/go-logr/logr"
	"k8s.io/klog/v2"

	"github.com/ducnt114/kboba/internal/k8s"
	"github.com/ducnt114/kboba/internal/ui"
)

func main() {
	var (
		kubeconfig = flag.String("kubeconfig", "", "path to the kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
		context    = flag.String("context", "", "kubeconfig context to use (default: current-context)")
		namespace  = flag.String("namespace", "", "namespace to start in (default: the context's namespace)")
	)
	flag.Parse()

	// client-go logs through klog to stderr, which would corrupt the TUI.
	// Errors we care about are surfaced in the status bar instead.
	klog.SetLogger(logr.Discard())

	// Fail early with a readable message if the kubeconfig can't be loaded.
	if _, err := k8s.NewClient(*kubeconfig, *context); err != nil {
		fmt.Fprintln(os.Stderr, "kboba:", err)
		os.Exit(1)
	}

	newClient := func(contextName string) (k8s.Client, error) {
		return k8s.NewClient(*kubeconfig, contextName)
	}
	model := ui.New(newClient, ui.Options{Context: *context, Namespace: *namespace})

	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "kboba:", err)
		os.Exit(1)
	}
}
