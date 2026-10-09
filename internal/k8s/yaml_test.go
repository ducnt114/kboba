package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetYAML(t *testing.T) {
	p := pod("a", "web")
	p.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}
	c, _ := newFakeClient(t, p)

	out, err := c.GetYAML(context.Background(), Pods, "a", "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"apiVersion: v1\n", "kind: Pod\n", "name: web\n", "namespace: a\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "managedFields") {
		t.Errorf("managedFields should be stripped:\n%s", out)
	}
}

func TestGetYAMLClusterScoped(t *testing.T) {
	c, _ := newFakeClient(t, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}})

	// The namespace is ignored for cluster-scoped types.
	out, err := c.GetYAML(context.Background(), Nodes, "whatever", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "kind: Node\n") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestGetYAMLAppsGroup(t *testing.T) {
	out, err := toYAML(Deployments, deploymentFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "apiVersion: apps/v1\nkind: Deployment\n") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestGetYAMLNotFound(t *testing.T) {
	c, _ := newFakeClient(t)
	if _, err := c.GetYAML(context.Background(), Services, "a", "gone"); err == nil {
		t.Fatal("expected not found")
	}
}
