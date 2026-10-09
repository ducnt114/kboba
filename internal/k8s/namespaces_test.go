package k8s

import (
	"context"
	"errors"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ns(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func TestListNamespacesSorted(t *testing.T) {
	c, _ := newFakeClient(t, ns("kube-system"), ns("default"), ns("apps"))

	got, err := c.ListNamespaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"apps", "default", "kube-system"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListNamespacesReturnsConnErr(t *testing.T) {
	c, _ := newFakeClient(t)
	c.connErr = errors.New("boom")

	if _, err := c.ListNamespaces(context.Background()); err == nil {
		t.Fatal("expected connErr")
	}
}
