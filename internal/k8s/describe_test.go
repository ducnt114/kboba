package k8s

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func event(name, involved, reason, msg string) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: "a", Name: name},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "a", Name: involved},
		Reason:         reason,
		Message:        msg,
		Type:           "Warning",
		LastTimestamp:  metav1.NewTime(time.Now().Add(-time.Minute)),
	}
}

func TestDescribePod(t *testing.T) {
	p := pod("a", "web")
	p.Spec.NodeName = "node-1"
	p.Labels = map[string]string{"app": "web", "tier": "front"}
	p.Spec.Containers[0].Image = "nginx:1.27"
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:         "app",
		RestartCount: 4,
		State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1},
		},
	}}
	p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}

	c, _ := newFakeClient(t, p,
		event("e1", "web", "BackOff", "Back-off restarting failed container"),
		event("e2", "other", "Pulled", "should not appear"),
	)

	out, err := c.DescribePod(context.Background(), "a", "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Name:", "web",
		"Node:", "node-1",
		"app=web,tier=front",
		"CrashLoopBackOff",
		"nginx:1.27",
		"Restart Count:", "4",
		"Terminated (Error, exit code 1",
		"Ready", "False",
		"BackOff", "Back-off restarting failed container",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "should not appear") {
		t.Errorf("events of other pods must be excluded:\n%s", out)
	}
}

func TestDescribePodNotFound(t *testing.T) {
	c, _ := newFakeClient(t)
	if _, err := c.DescribePod(context.Background(), "a", "gone"); err == nil {
		t.Fatal("expected error for missing pod")
	}
}

func TestFormatPodDescriptionEventsError(t *testing.T) {
	out := formatPodDescription(pod("a", "web"), nil, errors.New("events is forbidden"), time.Now())
	if !strings.Contains(out, "unable to list events: events is forbidden") {
		t.Fatalf("output:\n%s", out)
	}
}
