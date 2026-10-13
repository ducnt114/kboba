package k8s

import (
	"context"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func owned(p *corev1.Pod, uid string, labels map[string]string, ownerUID, ownerKind string) *corev1.Pod {
	p.UID = types.UID(uid)
	p.Labels = labels
	if ownerUID != "" {
		p.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: "x", UID: types.UID(ownerUID)}}
	}
	return p
}

func TestListResources(t *testing.T) {
	c, _ := newFakeClient(t,
		owned(pod("a", "web-2"), "p2", map[string]string{"app": "web"}, "rs1", "ReplicaSet"),
		owned(pod("a", "web-1"), "p1", map[string]string{"app": "web"}, "rs1", "ReplicaSet"),
		owned(pod("a", "db"), "p3", map[string]string{"app": "db"}, "", ""),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
	)
	ctx := context.Background()

	got, err := c.ListResources(ctx, Pods, "a", "app=web")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "web-1" || got[1].Name != "web-2" {
		t.Fatalf("got %+v", got)
	}
	if got[0].UID != "p1" || len(got[0].Owners) != 1 || got[0].Owners[0] != (OwnerRef{Kind: "ReplicaSet", Name: "x", UID: "rs1"}) {
		t.Fatalf("uid/owners = %q %+v", got[0].UID, got[0].Owners)
	}

	nodes, err := c.ListResources(ctx, Nodes, "ignored", "")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes = %v, %v", nodes, err)
	}
}

func TestListResourcesDynamic(t *testing.T) {
	c, _ := newFakeDiscoveryClient(t, widgetCRD(), widget("a", "w1", 1, "True"), widget("b", "w2", 2, "True"))
	rt, err := c.ResolveResourceType(context.Background(), "widgets")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ListResources(context.Background(), rt, "a", "")
	if err != nil || len(got) != 1 || got[0].Name != "w1" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestOwnerTree(t *testing.T) {
	r := func(name, uid string, owners ...string) Resource {
		res := Resource{Name: name, UID: uid}
		for _, o := range owners {
			res.Owners = append(res.Owners, OwnerRef{UID: o})
		}
		return res
	}
	deploy := r("web", "d1")
	rss := []Resource{r("web-new", "rs2", "d1"), r("web-old", "rs1", "d1"), r("other", "rs9", "d9")}
	pods := []Resource{
		r("web-new-a", "p1", "rs2"),
		r("web-new-b", "p2", "rs2"),
		r("web-old-a", "p3", "rs1"),
		r("orphan", "p4"),          // no owner
		r("stranger", "p5", "rs9"), // owned by an RS outside this tree
	}

	tree := OwnerTree(Deployments, deploy, Level{ReplicaSets, rss}, Level{Pods, pods})

	names := func(nodes []*TreeNode) []string {
		var out []string
		for _, n := range nodes {
			out = append(out, n.Resource.Name)
		}
		return out
	}
	if got := names(tree.Children); !reflect.DeepEqual(got, []string{"web-new", "web-old"}) {
		t.Fatalf("replicasets = %v", got)
	}
	if got := names(tree.Children[0].Children); !reflect.DeepEqual(got, []string{"web-new-a", "web-new-b"}) {
		t.Fatalf("pods of web-new = %v", got)
	}
	if got := names(tree.Children[1].Children); !reflect.DeepEqual(got, []string{"web-old-a"}) {
		t.Fatalf("pods of web-old = %v", got)
	}
	if tree.Children[0].Type != ReplicaSets || tree.Children[0].Children[0].Type != Pods {
		t.Fatal("node types not set")
	}
}

func TestReplicaSetCells(t *testing.T) {
	replicas := int32(3)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "web-abc"},
		Spec: appsv1.ReplicaSetSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "web", "pod-template-hash": "abc"},
		}},
		Status: appsv1.ReplicaSetStatus{Replicas: 3, ReadyReplicas: 2},
	}
	r := replicaSetResource(rs)
	if !reflect.DeepEqual(r.Cells, []string{"web-abc", "3", "3", "2"}) || r.Selector != "app=web,pod-template-hash=abc" {
		t.Fatalf("cells=%v selector=%q", r.Cells, r.Selector)
	}
}
