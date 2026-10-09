// Package k8s is kboba's read-only gateway to a Kubernetes cluster.
//
// Everything in this package only ever issues get, list and watch requests.
// It deliberately knows nothing about the terminal UI so it can be tested
// (and reasoned about) on its own.
package k8s

import (
	"context"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Client is the complete set of operations the UI may perform against a
// cluster. It is intentionally read-only: adding a method that mutates the
// cluster will fail TestClientInterfaceIsReadOnly.
type Client interface {
	// ListContexts returns all contexts from the kubeconfig. The context this
	// client is bound to is marked as Current. It never talks to the cluster.
	ListContexts() ([]ContextInfo, error)

	// ListNamespaces returns the namespace names, sorted.
	ListNamespaces(ctx context.Context) ([]string, error)

	// ListPods returns the pods of namespace ("" for all), sorted.
	ListPods(ctx context.Context, namespace string) ([]PodInfo, error)

	// WatchPods starts an informer that streams pod changes in namespace
	// ("" for all). The caller must call Stop on the returned watch.
	WatchPods(namespace string) (*PodWatch, error)

	// GetPod returns a single pod.
	GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error)

	// StreamLogs follows a container's logs until ctx is cancelled.
	StreamLogs(ctx context.Context, namespace, pod, container string) (<-chan string, <-chan error, error)
}

// client is the real Client implementation. It is bound to a single
// kubeconfig context; switching context means creating a new client.
type client struct {
	raw         clientcmdapi.Config
	contextName string
	clientset   kubernetes.Interface
	// connErr is set when the context cannot be used (e.g. it does not exist).
	// Cluster calls return it, while ListContexts still works so the user can
	// pick another context.
	connErr error
}

// NewClient loads the kubeconfig (path may be empty to use the default
// loading rules: $KUBECONFIG, then ~/.kube/config) and binds a client to
// contextName (empty means the kubeconfig's current-context).
//
// The kubeconfig file is only ever read, never written: switching context in
// kboba is purely in-memory state.
//
// An error is returned only when the kubeconfig itself cannot be loaded.
// Problems with the selected context are reported lazily by cluster calls.
func NewClient(kubeconfigPath, contextName string) (Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	raw, err := cc.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	if contextName == "" {
		contextName = raw.CurrentContext
	}

	c := &client{raw: raw, contextName: contextName}
	if _, ok := raw.Contexts[contextName]; !ok {
		c.connErr = fmt.Errorf("context %q not found in kubeconfig", contextName)
		return c, nil
	}

	cfg, err := cc.ClientConfig()
	if err != nil {
		c.connErr = fmt.Errorf("context %q: %w", contextName, err)
		return c, nil
	}
	cfg.Wrap(newReadOnlyTransport)

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		c.connErr = fmt.Errorf("context %q: %w", contextName, err)
		return c, nil
	}
	c.clientset = cs
	return c, nil
}

// readOnlyTransport is a defence-in-depth guard: it refuses any HTTP request
// that could modify the cluster. Reads, watches and log streaming are all GET.
type readOnlyTransport struct {
	next http.RoundTripper
}

func newReadOnlyTransport(next http.RoundTripper) http.RoundTripper {
	return &readOnlyTransport{next: next}
}

func (t *readOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, fmt.Errorf("kboba is read-only: refusing %s %s", req.Method, req.URL.Path)
	}
	return t.next.RoundTrip(req)
}
