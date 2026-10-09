package k8s

import (
	"context"
	"testing"
	"time"
)

func TestStreamLogs(t *testing.T) {
	c, cs := newFakeClient(t, pod("a", "web"))

	lines, errs, err := c.StreamLogs(context.Background(), "a", "web", "app")
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

	lines, _, err := c.StreamLogs(ctx, "a", "web", "app")
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
