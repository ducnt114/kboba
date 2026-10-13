package k8s

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func eventFor(ns, name, uid, reason, msg string) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: ns, Name: name},
		InvolvedObject: corev1.ObjectReference{UID: types.UID(uid)},
		Reason:         reason, Message: msg, Type: "Normal",
		LastTimestamp: metav1.NewTime(time.Now().Add(-time.Minute)),
	}
}

func TestDescribeDeployment(t *testing.T) {
	d := deploymentFixture()
	d.UID = "d-new"
	d.Labels = map[string]string{"app": "web"}
	d.Annotations = map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": `{"huge":"blob"}`,
		"team": "payments",
	}
	d.Status.Conditions = []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: "NewReplicaSetAvailable", LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Hour))},
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, Reason: "MinimumReplicasUnavailable", Message: "Deployment does not have minimum availability."},
	}
	c, _ := newFakeClient(t, d,
		eventFor("a", "web.1", "d-new", "ScalingReplicaSet", "Scaled up replica set web-abc to 1"),
		eventFor("a", "web.0", "d-old", "ScalingReplicaSet", "from a deleted deployment with the same name"),
	)

	out, err := c.Describe(context.Background(), Deployments, "a", "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Kind:", "Deployment", "API Version:", "apps/v1",
		"app=web", "team=payments",
		"Available", "False", "MinimumReplicasUnavailable", "Deployment does not have minimum availability.",
		"Progressing", "NewReplicaSetAvailable", "60m", // kubectl-style duration
		"Scaled up replica set web-abc to 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"last-applied", "huge", "deleted deployment"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("%q should not appear:\n%s", unwanted, out)
		}
	}
}

func TestDescribeClusterScopedFindsEventsAnywhere(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1", UID: "n-1"}}
	c, _ := newFakeClient(t, node, eventFor("default", "n1.1", "n-1", "NodeReady", "Node n1 status is now: NodeReady"))

	out, err := c.Describe(context.Background(), Nodes, "", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Namespace:") || !strings.Contains(out, "Node n1 status is now: NodeReady") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestDescribeCRDAndSecret(t *testing.T) {
	w := widget("a", "w1", 3, "False")
	w.SetUID("w-1")
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"namespace": "a", "name": "db", "annotations": map[string]any{
			"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"aHVudGVyMg=="}}`,
		}},
		"data": map[string]any{"password": "aHVudGVyMg=="},
	}}
	c, _ := newFakeDiscoveryClient(t, widgetCRD(), w, secret)
	ctx := context.Background()

	rt, _ := c.ResolveResourceType(ctx, "widgets")
	out, err := c.Describe(ctx, rt, "a", "w1")
	if err != nil {
		t.Fatal(err)
	}
	// The widget's conditions come from status.conditions, generically.
	if !strings.Contains(out, "Widget") || !strings.Contains(out, "Synced") || !strings.Contains(out, "Ready") {
		t.Fatalf("widget:\n%s", out)
	}

	rt, _ = c.ResolveResourceType(ctx, "secrets")
	out, err = c.Describe(ctx, rt, "a", "db")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "aHVudGVyMg==") {
		t.Fatalf("secret value leaked:\n%s", out)
	}
}

func TestDescribePodsUsesPodDescription(t *testing.T) {
	c, _ := newFakeClient(t, pod("a", "web"))
	out, err := c.Describe(context.Background(), Pods, "a", "web")
	if err != nil || !strings.Contains(out, "Containers:") {
		t.Fatalf("got %v:\n%s", err, out)
	}
}
