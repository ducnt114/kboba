package k8s

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// newFakeClient returns a client backed by client-go's fake clientset,
// seeded with objs. The fake records every action it receives, which lets
// tests assert that only reads are performed.
func newFakeClient(t *testing.T, objs ...runtime.Object) (*client, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset(objs...)
	raw := clientcmdapi.Config{
		CurrentContext: "test",
		Contexts:       map[string]*clientcmdapi.Context{"test": {Cluster: "c", AuthInfo: "u"}},
	}
	return &client{raw: raw, contextName: "test", clientset: cs}, cs
}
