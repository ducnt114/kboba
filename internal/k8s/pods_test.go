package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestPodStatus(t *testing.T) {
	waiting := func(reason string) corev1.ContainerState {
		return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}
	}
	terminated := func(reason string, code int32) corev1.ContainerState {
		return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason, ExitCode: code}}
	}
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}

	tests := []struct {
		name   string
		mutate func(p *corev1.Pod)
		want   string
	}{
		{"running", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: running, Ready: true}}
		}, "Running"},
		{"pending without phase", func(p *corev1.Pod) { p.Status.Phase = "" }, "Pending"},
		{"crashloop", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: waiting("CrashLoopBackOff")}}
		}, "CrashLoopBackOff"},
		{"oomkilled", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: terminated("OOMKilled", 137)}}
		}, "OOMKilled"},
		{"exit code without reason", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: terminated("", 2)}}
		}, "ExitCode:2"},
		{"evicted", func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodFailed
			p.Status.Reason = "Evicted"
		}, "Evicted"},
		{"terminating", func(p *corev1.Pod) {
			now := metav1.Now()
			p.DeletionTimestamp = &now
		}, "Terminating"},
		{"init running", func(p *corev1.Pod) {
			p.Spec.InitContainers = []corev1.Container{{Name: "a"}, {Name: "b"}}
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{
				{State: terminated("Completed", 0)},
				{State: running},
			}
		}, "Init:1/2"},
		{"init crashloop", func(p *corev1.Pod) {
			p.Spec.InitContainers = []corev1.Container{{Name: "a"}}
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{{State: waiting("CrashLoopBackOff")}}
		}, "Init:CrashLoopBackOff"},
		{"init failed", func(p *corev1.Pod) {
			p.Spec.InitContainers = []corev1.Container{{Name: "a"}}
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{{State: terminated("Error", 1)}}
		}, "Init:Error"},
		{"sidecar still running after main completed", func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{
				{State: running, Ready: true},
				{State: terminated("Completed", 0)},
			}
		}, "Running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := pod("ns", "p")
			tt.mutate(p)
			if got := podStatus(p); got != tt.want {
				t.Errorf("podStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewPodInfo(t *testing.T) {
	p := pod("ns", "web")
	p.Spec.Containers = []corev1.Container{{Name: "app"}, {Name: "sidecar"}}
	p.Annotations = map[string]string{defaultContainerAnnotation: "sidecar"}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: "app", Ready: true, RestartCount: 3},
		{Name: "sidecar", Ready: false, RestartCount: 2},
	}

	info := NewPodInfo(p)
	if info.Ready != "1/2" {
		t.Errorf("Ready = %q", info.Ready)
	}
	if info.Restarts != 5 {
		t.Errorf("Restarts = %d", info.Restarts)
	}
	if info.DefaultContainer != "sidecar" {
		t.Errorf("DefaultContainer = %q", info.DefaultContainer)
	}
	if info.Key() != "ns/web" {
		t.Errorf("Key = %q", info.Key())
	}

	// An annotation naming a non-existent container is ignored.
	p.Annotations[defaultContainerAnnotation] = "nope"
	if got := NewPodInfo(p).DefaultContainer; got != "app" {
		t.Errorf("DefaultContainer = %q, want app", got)
	}
}

func TestListPodsFiltersNamespace(t *testing.T) {
	c, _ := newFakeClient(t, pod("a", "p2"), pod("a", "p1"), pod("b", "p3"))
	ctx := context.Background()

	got, err := c.ListPods(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "p1" || got[1].Name != "p2" {
		t.Fatalf("ListPods(a) = %+v", got)
	}

	all, err := c.ListPods(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("ListPods(all) returned %d pods", len(all))
	}
}

// nextEvent waits for the next event of the given type, skipping others.
func nextEvent(t *testing.T, ch <-chan PodEvent, typ PodEventType) PodEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("events closed while waiting for type %d", typ)
			}
			if ev.Type == typ {
				return ev
			}
		case <-timeout:
			t.Fatalf("timed out waiting for event type %d", typ)
		}
	}
}

func TestWatchPods(t *testing.T) {
	c, cs := newFakeClient(t, pod("a", "existing"))

	w, err := c.WatchPods("a")
	if err != nil {
		t.Fatal(err)
	}

	if ev := nextEvent(t, w.Events, PodUpserted); ev.Pod.Name != "existing" {
		t.Fatalf("first event for %q", ev.Pod.Name)
	}
	nextEvent(t, w.Events, PodsSynced)

	// Simulate cluster activity. These writes go to the *fake* clientset;
	// kboba itself never writes.
	ctx := context.Background()
	if _, err := cs.CoreV1().Pods("a").Create(ctx, pod("a", "new"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, w.Events, PodUpserted); ev.Pod.Name != "new" {
		t.Fatalf("expected add of %q, got %q", "new", ev.Pod.Name)
	}
	if err := cs.CoreV1().Pods("a").Delete(ctx, "existing", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, w.Events, PodDeleted); ev.Pod.Name != "existing" {
		t.Fatalf("expected delete of %q, got %q", "existing", ev.Pod.Name)
	}

	// Stop must eventually close the channel, and be idempotent.
	w.Stop()
	w.Stop()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-w.Events:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("Events not closed after Stop")
		}
	}
}
