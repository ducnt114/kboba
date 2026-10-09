package k8s

import (
	"context"
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nextEvent waits for the next event of the given type, skipping others.
func nextEvent(t *testing.T, ch <-chan ResourceEvent, typ ResourceEventType) ResourceEvent {
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

func TestWatchResourcesPods(t *testing.T) {
	c, cs := newFakeClient(t, pod("a", "existing"))

	w, err := c.WatchResources(Pods, "a")
	if err != nil {
		t.Fatal(err)
	}

	ev := nextEvent(t, w.Events, Upserted)
	if ev.Resource.Name != "existing" || ev.Resource.Pod == nil {
		t.Fatalf("first event = %+v", ev.Resource)
	}
	nextEvent(t, w.Events, Synced)

	// Simulate cluster activity. These writes go to the *fake* clientset;
	// kboba itself never writes.
	ctx := context.Background()
	if _, err := cs.CoreV1().Pods("a").Create(ctx, pod("a", "new"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, w.Events, Upserted); ev.Resource.Name != "new" {
		t.Fatalf("expected add of %q, got %q", "new", ev.Resource.Name)
	}
	if err := cs.CoreV1().Pods("a").Delete(ctx, "existing", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, w.Events, Deleted); ev.Resource.Name != "existing" {
		t.Fatalf("expected delete of %q, got %q", "existing", ev.Resource.Name)
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

func TestWatchResourcesClusterScopedIgnoresNamespace(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	c, _ := newFakeClient(t, node)

	w, err := c.WatchResources(Nodes, "some-namespace")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	if ev := nextEvent(t, w.Events, Upserted); ev.Resource.Name != "n1" {
		t.Fatalf("got %+v", ev.Resource)
	}
}

func TestEveryTypeHasMatchingCells(t *testing.T) {
	replicas := int32(3)
	objs := map[*ResourceType]any{
		Pods:        pod("a", "p"),
		Deployments: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "d"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}},
		Services:    &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "s"}},
		Events:      &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e"}},
		Nodes:       &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}},
	}
	for _, rt := range ResourceTypes() {
		r, ok := rt.convert(objs[rt])
		if !ok {
			t.Errorf("%s: convert failed", rt.Name)
			continue
		}
		if len(r.Cells) != len(rt.Columns) {
			t.Errorf("%s: %d cells for %d columns", rt.Name, len(r.Cells), len(rt.Columns))
		}
	}
	// A wrong object type is rejected rather than panicking.
	if _, ok := Pods.convert(&corev1.Node{}); ok {
		t.Error("pods converter accepted a node")
	}
}

func TestDeploymentCells(t *testing.T) {
	replicas := int32(3)
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 2, UpdatedReplicas: 3, AvailableReplicas: 2},
	}
	want := []string{"web", "2/3", "3", "2"}
	if got := deploymentResource(d).Cells; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestServiceCells(t *testing.T) {
	tests := []struct {
		name         string
		svc          corev1.Service
		wantExternal string
		wantPorts    string
	}{
		{
			name: "cluster ip",
			svc: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1",
				Ports: []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
			}},
			wantExternal: "<none>", wantPorts: "80/TCP",
		},
		{
			name: "pending load balancer with node port",
			svc: corev1.Service{Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{{Port: 443, NodePort: 30443, Protocol: corev1.ProtocolTCP}, {Port: 53, Protocol: corev1.ProtocolUDP}},
			}},
			wantExternal: "<pending>", wantPorts: "443:30443/TCP,53/UDP",
		},
		{
			name: "load balancer with ingress",
			svc: corev1.Service{
				Spec:   corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
				Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "1.2.3.4"}}}},
			},
			wantExternal: "1.2.3.4", wantPorts: "<none>",
		},
	}
	for _, tt := range tests {
		cells := serviceResource(&tt.svc).Cells
		if cells[3] != tt.wantExternal || cells[4] != tt.wantPorts {
			t.Errorf("%s: external=%q ports=%q", tt.name, cells[3], cells[4])
		}
	}
}

func TestNodeCells(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{
			"node-role.kubernetes.io/control-plane": "",
			"node-role.kubernetes.io/etcd":          "",
			"kubernetes.io/hostname":                "n1",
		}},
		Spec: corev1.NodeSpec{Unschedulable: true},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.33.0"},
		},
	}
	want := []string{"n1", "Ready,SchedulingDisabled", "control-plane,etcd", "v1.33.0"}
	if got := nodeResource(n).Cells; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEventCellsUseLastSeen(t *testing.T) {
	last := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "web.123", CreationTimestamp: metav1.NewTime(last.Add(-time.Hour))},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web"},
		Type:           "Warning", Reason: "BackOff", Message: " restarting \n",
		LastTimestamp: metav1.NewTime(last),
	}
	r := eventResource(ev)
	want := []string{"Warning", "BackOff", "pod/web", "restarting"}
	if !reflect.DeepEqual(r.Cells, want) {
		t.Fatalf("cells = %v", r.Cells)
	}
	if !r.Created.Equal(last) {
		t.Fatalf("Created = %v, want last seen %v", r.Created, last)
	}
}

func TestLookupResourceType(t *testing.T) {
	for alias, want := range map[string]*ResourceType{
		"pods": Pods, "po": Pods, "deploy": Deployments, "svc": Services, "ev": Events, "no": Nodes,
	} {
		if got, ok := LookupResourceType(alias); !ok || got != want {
			t.Errorf("LookupResourceType(%q) = %v, %v", alias, got, ok)
		}
	}
	if _, ok := LookupResourceType("secrets"); ok {
		t.Error("unknown type should not be found")
	}
}

func deploymentFixture() *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "web"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
}
