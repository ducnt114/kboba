package k8s

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
)

// readOnlyMethods is the exhaustive allowlist of Client methods. Every one of
// them must only perform get/list/watch. If you add a method to Client, you
// must consciously add it here too, and it must be a read.
var readOnlyMethods = []string{
	"ListContexts",
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
