package k8s

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

func usage(cpu, mem string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
}

func TestListMetricsPods(t *testing.T) {
	web := &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "web", Labels: map[string]string{"app": "web"}},
		Containers: []metricsv1beta1.ContainerMetrics{
			{Name: "app", Usage: usage("250m", "100Mi")},
			{Name: "sidecar", Usage: usage("50m", "28Mi")},
		},
	}
	other := &metricsv1beta1.PodMetrics{ObjectMeta: metav1.ObjectMeta{Namespace: "b", Name: "db"}}
	c, _, mc := newFakeClientWithMetrics(t, nil, web, other)

	got, err := c.ListMetrics(context.Background(), Pods, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{CPUMilli: 300, MemoryBytes: 128 * 1024 * 1024}
	if len(got) != 1 || got["a/web"] != want {
		t.Fatalf("got %+v, want a/web=%+v", got, want)
	}

	// Only list requests on metrics.k8s.io.
	for _, a := range mc.Actions() {
		if a.GetVerb() != "list" || a.GetResource().Group != "metrics.k8s.io" {
			t.Errorf("unexpected action %s %s", a.GetVerb(), a.GetResource())
		}
	}
}

func TestListMetricsNodes(t *testing.T) {
	n := &metricsv1beta1.NodeMetrics{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Usage: usage("1500m", "2Gi")}
	c, _, _ := newFakeClientWithMetrics(t, nil, n)

	got, err := c.ListMetrics(context.Background(), Nodes, "ignored", "")
	if err != nil {
		t.Fatal(err)
	}
	if u := got["/n1"]; u.CPUMilli != 1500 || u.MemoryBytes != 2<<30 {
		t.Fatalf("got %+v", got)
	}
}

func TestListMetricsUnsupportedType(t *testing.T) {
	c, _, _ := newFakeClientWithMetrics(t, nil)
	if _, err := c.ListMetrics(context.Background(), Services, "a", ""); !errors.Is(err, ErrNoMetrics) {
		t.Fatalf("err = %v", err)
	}
}
