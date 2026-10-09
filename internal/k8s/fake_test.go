package k8s

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// newFakeClient returns a client backed by client-go's fake clientset,
// seeded with objs. The fake records every action it receives, which lets
// tests assert that only reads are performed.
func newFakeClient(t *testing.T, objs ...runtime.Object) (*client, *fake.Clientset) {
	t.Helper()
	c, cs, _ := newFakeClientWithMetrics(t, objs)
	return c, cs
}

// newFakeClientWithMetrics also returns the fake metrics clientset, seeded
// with metricsObjs (PodMetrics / NodeMetrics).
func newFakeClientWithMetrics(t *testing.T, objs []runtime.Object, metricsObjs ...runtime.Object) (*client, *fake.Clientset, *metricsfake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset(objs...)

	// NewSimpleClientset(objs...) would guess the resource name from the
	// kind ("podmetricses"), but the metrics API (and its fake) uses "pods"
	// and "nodes". Add the objects under the right names explicitly.
	mc := metricsfake.NewSimpleClientset()
	for _, o := range metricsObjs {
		var err error
		switch m := o.(type) {
		case *metricsv1beta1.PodMetrics:
			err = mc.Tracker().Create(metricsv1beta1.SchemeGroupVersion.WithResource("pods"), m, m.Namespace)
		case *metricsv1beta1.NodeMetrics:
			err = mc.Tracker().Create(metricsv1beta1.SchemeGroupVersion.WithResource("nodes"), m, "")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	raw := clientcmdapi.Config{
		CurrentContext: "test",
		Contexts:       map[string]*clientcmdapi.Context{"test": {Cluster: "c", AuthInfo: "u"}},
	}
	return &client{raw: raw, contextName: "test", clientset: cs, metrics: mc}, cs, mc
}
