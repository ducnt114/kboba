package k8s

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// readOnlyMethods is the exhaustive allowlist of Client methods. Every one of
// them must only perform get/list/watch. If you add a method to Client, you
// must consciously add it here too, and it must be a read.
var readOnlyMethods = []string{
	"ListContexts",
	"ListNamespaces",
	"ListPods",
	"WatchResources",
	"GetYAML",
	"ListMetrics",
	"ResolveResourceType",
	"GetPod",
	"StreamLogs",
	"DescribePod",
}

func TestClientInterfaceIsReadOnly(t *testing.T) {
	typ := reflect.TypeOf((*Client)(nil)).Elem()

	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	want := append([]string(nil), readOnlyMethods...)
	sort.Strings(got)
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Client methods changed.\n got: %v\nwant: %v\n"+
			"Only read operations (get/list/watch) are allowed.", got, want)
	}
}

func TestReadOnlyTransportRejectsWrites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	rt := newReadOnlyTransport(http.DefaultTransport)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, _ := http.NewRequest(method, srv.URL, nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Errorf("%s should be allowed, got %v", method, err)
			continue
		}
		resp.Body.Close()
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(method, srv.URL, nil)
		if _, err := rt.RoundTrip(req); err == nil {
			t.Errorf("%s should be rejected", method)
		}
	}
}

// TestClientOnlyReads calls every Client method against the fake clientset
// and checks that the recorded API actions are all get, list or watch.
// TestDynamicClientOnlyReads does the same for discovery and the dynamic
// client: resolving a CRD, watching it and reading its YAML.
func TestDynamicClientOnlyReads(t *testing.T) {
	c, dc := newFakeDiscoveryClient(t, widgetCRD(), widget("a", "w1", 1, "True"))
	ctx := context.Background()

	rt, err := c.ResolveResourceType(ctx, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.WatchResources(rt, "", "")
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, w.Events, Synced)
	w.Stop()
	if _, err := c.GetYAML(ctx, rt, "a", "w1"); err != nil {
		t.Fatal(err)
	}

	actions := append(dc.Actions(), c.clientset.(*fake.Clientset).Actions()...)
	if len(actions) == 0 {
		t.Fatal("expected some actions")
	}
	for _, a := range actions {
		if v := a.GetVerb(); v != "get" && v != "list" && v != "watch" {
			t.Errorf("non-read action: %s %s", v, a.GetResource())
		}
	}
}

func TestClientOnlyReads(t *testing.T) {
	c, cs := newFakeClient(t, ns("default"), pod("default", "web"))
	ctx := context.Background()

	if _, err := c.ListContexts(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListNamespaces(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListPods(ctx, "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetPod(ctx, "default", "web"); err != nil {
		t.Fatal(err)
	}
	for _, rt := range ResourceTypes() {
		w, err := c.WatchResources(rt, "", "")
		if err != nil {
			t.Fatal(err)
		}
		nextEvent(t, w.Events, Synced)
		w.Stop()
	}
	lines, _, err := c.StreamLogs(ctx, "default", "web", "app", LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range lines {
	}
	if _, err := c.DescribePod(ctx, "default", "web"); err != nil {
		t.Fatal(err)
	}
	for _, rt := range ResourceTypes() {
		// Not-found is fine: we only care about the verb that was used.
		_, _ = c.GetYAML(ctx, rt, "default", "web")
		_, _ = c.ListMetrics(ctx, rt, "default", "")
	}
	for _, a := range c.metrics.(*metricsfake.Clientset).Actions() {
		if v := a.GetVerb(); v != "get" && v != "list" && v != "watch" {
			t.Errorf("non-read metrics action: %s %s", v, a.GetResource().Resource)
		}
	}

	actions := cs.Actions()
	if len(actions) == 0 {
		t.Fatal("expected some API actions to be recorded")
	}
	for _, a := range actions {
		switch a.GetVerb() {
		case "get", "list", "watch":
		default:
			t.Errorf("non-read action: %s %s", a.GetVerb(), a.GetResource().Resource)
		}
	}
}
