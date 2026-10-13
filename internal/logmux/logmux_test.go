package logmux

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

func collect(t *testing.T, ch <-chan Line, n int) []Line {
	t.Helper()
	var out []Line
	timeout := time.After(5 * time.Second)
	for len(out) < n {
		select {
		case l, ok := <-ch:
			if !ok {
				t.Fatalf("closed after %d of %d lines", len(out), n)
			}
			out = append(out, l)
		case <-timeout:
			t.Fatalf("timed out after %d of %d lines", len(out), n)
		}
	}
	return out
}

func source(lines ...string) <-chan string {
	ch := make(chan string, len(lines))
	for _, l := range lines {
		ch <- l
	}
	close(ch)
	return ch
}

func TestMergesSourcesAndReportsEnds(t *testing.T) {
	m := New(context.Background())
	defer m.Close()

	errs := make(chan error, 1)
	errs <- errors.New("connection reset")
	m.Add("web-1", source("a", "b"), nil)
	m.Add("web-2", source("c"), errs)

	got := collect(t, m.Lines(), 5) // 3 lines + 2 ends
	var texts []string
	ends := map[string]error{}
	for _, l := range got {
		if l.Ended {
			ends[l.Source] = l.Err
			continue
		}
		texts = append(texts, l.Source+":"+l.Text)
	}
	sort.Strings(texts)
	if want := []string{"web-1:a", "web-1:b", "web-2:c"}; len(texts) != 3 || texts[0] != want[0] || texts[1] != want[1] || texts[2] != want[2] {
		t.Fatalf("lines = %v", texts)
	}
	if _, ok := ends["web-1"]; !ok || ends["web-1"] != nil {
		t.Errorf("web-1 end = %v", ends["web-1"])
	}
	if ends["web-2"] == nil || ends["web-2"].Error() != "connection reset" {
		t.Errorf("web-2 end = %v", ends["web-2"])
	}
}

func TestPerSourceOrderIsKept(t *testing.T) {
	m := New(context.Background())
	defer m.Close()
	m.Add("p", source("1", "2", "3", "4"), nil)
	got := collect(t, m.Lines(), 4)
	for i, want := range []string{"1", "2", "3", "4"} {
		if got[i].Text != want {
			t.Fatalf("got %v", got)
		}
	}
}

func TestCloseReleasesReaderAndBlockedForwarders(t *testing.T) {
	m := New(context.Background())
	// A source that never ends, and nobody reading: the forwarder blocks
	// on send once the buffer is full.
	endless := make(chan string)
	go func() {
		for {
			select {
			case endless <- "x":
			case <-time.After(time.Second):
				return
			}
		}
	}()
	m.Add("busy", endless, nil)
	time.Sleep(50 * time.Millisecond)

	m.Close()
	m.Close() // idempotent

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-m.Lines():
			if !ok {
				// Adding after Close is a no-op, not a panic.
				m.Add("late", source("y"), nil)
				return
			}
		case <-deadline:
			t.Fatal("Lines() not closed after Close")
		}
	}
}

func TestContextCancelCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := New(ctx)
	cancel()
	select {
	case _, ok := <-m.Lines():
		if ok {
			t.Fatal("unexpected line")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context should close the mux")
	}
}
