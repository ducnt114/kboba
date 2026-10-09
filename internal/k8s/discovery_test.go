package k8s

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var widgetsGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

// discoveryLists is what a server with a "widgets" CRD would advertise.
var discoveryLists = []*metav1.APIResourceList{
	{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{Name: "pods", SingularName: "pod", Kind: "Pod", Namespaced: true, ShortNames: []string{"po"}, Verbs: metav1.Verbs{"get", "list", "watch"}},
			{Name: "pods/log", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get"}},
			{Name: "configmaps", SingularName: "configmap", Kind: "ConfigMap", Namespaced: true, ShortNames: []string{"cm"}, Verbs: metav1.Verbs{"get", "list", "watch"}},
			{Name: "secrets", SingularName: "secret", Kind: "Secret", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "watch"}},
			{Name: "bindings", SingularName: "binding", Kind: "Binding", Namespaced: true, Verbs: metav1.Verbs{"create"}},
		},
	},
	{
		GroupVersion: "apps/v1",
		APIResources: []metav1.APIResource{
			{Name: "deployments", SingularName: "deployment", Kind: "Deployment", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "watch"}},
		},
	},
	{
		GroupVersion: "example.com/v1",
		APIResources: []metav1.APIResource{
			{Name: "widgets", SingularName: "widget", Kind: "Widget", Namespaced: true, ShortNames: []string{"wd"}, Verbs: metav1.Verbs{"get", "list", "watch"}},
		},
	},
}

func widgetCRD() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "widgets.example.com"},
		"spec": map[string]any{
			"versions": []any{
				map[string]any{"name": "v1beta1", "additionalPrinterColumns": []any{
					map[string]any{"name": "Old", "jsonPath": ".spec.old"},
				}},
				map[string]any{"name": "v1", "additionalPrinterColumns": []any{
					map[string]any{"name": "Size", "type": "integer", "jsonPath": ".spec.size"},
					map[string]any{"name": "Ready", "type": "string", "jsonPath": `.status.conditions[?(@.type=="Ready")].status`},
					map[string]any{"name": "Detail", "jsonPath": ".status.detail", "priority": int64(1)}, // wide only
					map[string]any{"name": "Age", "type": "date", "jsonPath": ".metadata.creationTimestamp"},
					map[string]any{"name": "Broken", "jsonPath": ".spec[unclosed"},
				}},
			},
		},
	}}
}

func widget(ns, name string, size int64, ready string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"namespace": ns, "name": name},
		"spec":       map[string]any{"size": size},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Synced", "status": "True"},
			map[string]any{"type": "Ready", "status": ready},
		}},
	}}
}

// newFakeDiscoveryClient returns a client whose discovery advertises
// discoveryLists and whose dynamic client holds objs.
func newFakeDiscoveryClient(t *testing.T, objs ...runtime.Object) (*client, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	c, cs := newFakeClient(t)
	cs.Resources = discoveryLists
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			widgetsGVR:                              "WidgetList",
			crdGVR:                                  "CustomResourceDefinitionList",
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			secretsGVR:                              "SecretList",
		}, objs...)
	c.dynamic = dc
	return c, dc
}

func TestResolveResourceType(t *testing.T) {
	c, _ := newFakeDiscoveryClient(t, widgetCRD())
	ctx := context.Background()

	// Built-in views win, also when named "plural.group".
	for _, name := range []string{"po", "deployments.apps"} {
		rt, err := c.ResolveResourceType(ctx, name)
		if err != nil || rt.dynamic {
			t.Errorf("%s: got %+v, %v; want a built-in type", name, rt, err)
		}
	}

	first, err := c.ResolveResourceType(ctx, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if !first.dynamic || first.gvr != widgetsGVR || !first.Namespaced || first.Title != "Widgets" {
		t.Fatalf("widgets = %+v", first)
	}
	for _, name := range []string{"widget", "wd", "Widget", "widgets.example.com"} {
		rt, err := c.ResolveResourceType(ctx, name)
		if err != nil || rt != first {
			t.Errorf("%s: got %p, %v; want the cached %p", name, rt, err, first)
		}
	}

	for _, name := range []string{"bindings", "pods/log", "nope"} { // can't watch / subresource / unknown
		if _, err := c.ResolveResourceType(ctx, name); err == nil {
			t.Errorf("%s should not resolve", name)
		}
	}
}

func TestPrinterColumns(t *testing.T) {
	c, _ := newFakeDiscoveryClient(t, widgetCRD())
	rt, err := c.ResolveResourceType(context.Background(), "widgets")
	if err != nil {
		t.Fatal(err)
	}
	// v1's columns only; wide, age and broken columns skipped.
	var titles []string
	for _, col := range rt.Columns {
		titles = append(titles, col.Title)
	}
	if strings.Join(titles, ",") != "NAME,SIZE,READY" {
		t.Fatalf("columns = %v", titles)
	}

	r, ok := rt.convert(widget("a", "w1", 3, "False"))
	if !ok || strings.Join(r.Cells, ",") != "w1,3,False" {
		t.Fatalf("cells = %v (ok=%v)", r.Cells, ok)
	}
}

func TestDynamicTypeWithoutCRDHasNameOnly(t *testing.T) {
	c, _ := newFakeDiscoveryClient(t) // configmaps: core group, no CRD
	rt, err := c.ResolveResourceType(context.Background(), "cm")
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Columns) != 1 || rt.Columns[0].Title != "NAME" {
		t.Fatalf("columns = %+v", rt.Columns)
	}
}

func TestWatchDynamicResources(t *testing.T) {
	c, _ := newFakeDiscoveryClient(t, widgetCRD(), widget("a", "w1", 3, "True"), widget("b", "w2", 1, "True"))
	rt, err := c.ResolveResourceType(context.Background(), "widgets")
	if err != nil {
		t.Fatal(err)
	}

	w, err := c.WatchResources(rt, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	ev := nextEvent(t, w.Events, Upserted)
	if ev.Resource.Key() != "a/w1" || strings.Join(ev.Resource.Cells, ",") != "w1,3,True" {
		t.Fatalf("event = %+v", ev.Resource)
	}
	nextEvent(t, w.Events, Synced) // w2 is in another namespace
}

func TestGetYAMLDynamicAndSecretRedaction(t *testing.T) {
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"namespace": "a", "name": "db", "annotations": map[string]any{
			"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"aHVudGVyMg=="}}`,
			"team": "data",
		}},
		"data": map[string]any{"password": "aHVudGVyMg=="},
	}}
	c, _ := newFakeDiscoveryClient(t, widgetCRD(), widget("a", "w1", 3, "True"), secret)
	ctx := context.Background()

	rt, _ := c.ResolveResourceType(ctx, "widgets")
	out, err := c.GetYAML(ctx, rt, "a", "w1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "apiVersion: example.com/v1\nkind: Widget\n") || !strings.Contains(out, "size: 3") {
		t.Fatalf("widget yaml:\n%s", out)
	}

	rt, _ = c.ResolveResourceType(ctx, "secrets")
	out, err = c.GetYAML(ctx, rt, "a", "db")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "aHVudGVyMg==") {
		t.Fatalf("secret value leaked:\n%s", out)
	}
	if !strings.Contains(out, "password: <redacted, 12 chars>") || !strings.Contains(out, "team: data") {
		t.Fatalf("secret yaml:\n%s", out)
	}
}
