package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8stesting "k8s.io/client-go/testing"
)

func TestStreamLogs(t *testing.T) {
	c, cs := newFakeClient(t, pod("a", "web"))

	lines, errs, err := c.StreamLogs(context.Background(), "a", "web", "app", LogOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for l := range lines { // the fake stream ends after its canned body
		got = append(got, l)
	}
	if len(got) != 1 || got[0] != "fake logs" {
		t.Fatalf("lines = %q", got)
	}
	select {
	case err := <-errs:
		t.Fatalf("unexpected error: %v", err)
	default:
	}

	// The request must be a follow GET on pods/log for the right container.
	a := cs.Actions()[0]
	if a.GetVerb() != "get" || a.GetSubresource() != "log" {
		t.Fatalf("action = %s %s/%s", a.GetVerb(), a.GetResource().Resource, a.GetSubresource())
	}
}

func TestStreamLogsCancel(t *testing.T) {
	c, _ := newFakeClient(t, pod("a", "web"))
	ctx, cancel := context.WithCancel(context.Background())

	lines, _, err := c.StreamLogs(ctx, "a", "web", "app", LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	// Whatever was buffered may still arrive, but the channel must close.
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("lines not closed after cancel")
		}
	}
}

func TestStreamLogsOptions(t *testing.T) {
	tests := []struct {
		opts       LogOptions
		wantFollow bool
	}{
		{LogOptions{}, true},
		{LogOptions{Timestamps: true}, true},
		{LogOptions{Previous: true}, false}, // a dead container never writes again
	}
	for _, tt := range tests {
		c, cs := newFakeClient(t, pod("a", "web"))
		lines, _, err := c.StreamLogs(context.Background(), "a", "web", "app", tt.opts)
		if err != nil {
			t.Fatal(err)
		}
		for range lines {
		}

		action := cs.Actions()[0].(k8stesting.GenericAction)
		got := action.GetValue().(*corev1.PodLogOptions)
		if got.Follow != tt.wantFollow || got.Timestamps != tt.opts.Timestamps ||
			got.Previous != tt.opts.Previous || got.Container != "app" {
			t.Errorf("opts %+v → PodLogOptions %+v", tt.opts, got)
		}
	}
}
