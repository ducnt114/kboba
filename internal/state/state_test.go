package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.yaml") // directory created on save

	s, err := Open(path)
	if err != nil {
		t.Fatalf("missing file should not be an error: %v", err)
	}
	if _, ok := s.LastNamespace("dev"); ok {
		t.Fatal("empty store should remember nothing")
	}

	s.SetLastNamespace("dev", "team-a")
	s.SetLastNamespace("prod", "") // all namespaces
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if ns, ok := s2.LastNamespace("dev"); !ok || ns != "team-a" {
		t.Errorf("dev = %q, %v", ns, ok)
	}
	// "" (all namespaces) is remembered, and differs from "nothing".
	if ns, ok := s2.LastNamespace("prod"); !ok || ns != "" {
		t.Errorf("prod = %q, %v", ns, ok)
	}
	if _, ok := s2.LastNamespace("staging"); ok {
		t.Error("unknown context should not be remembered")
	}

	// No temporary files are left behind.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want only state.yaml", len(entries))
	}
}

func TestCorruptFileStillUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	if err := os.WriteFile(path, []byte("namespaces: [this is: not a map"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if s == nil {
		t.Fatal("a usable store must be returned even on error")
	}
	s.SetLastNamespace("dev", "x")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err != nil {
		t.Fatalf("save should have repaired the file: %v", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.yaml"))
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.SetLastNamespace("dev", string(rune('a'+i)))
			if err := s.Save(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, ok := s.LastNamespace("dev"); !ok {
		t.Fatal("lost the namespace")
	}
}

func TestHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	s, _ := Open(path)

	s.AddHistory("deploy")
	s.AddHistory("deploy") // repeated: not duplicated
	s.AddHistory("")       // ignored
	s.AddHistory("ns kube-system")
	if got := s.History(); len(got) != 2 || got[0] != "deploy" || got[1] != "ns kube-system" {
		t.Fatalf("history = %v", got)
	}

	// The returned slice is a copy.
	h := s.History()
	h[0] = "changed"
	if s.History()[0] != "deploy" {
		t.Fatal("History must return a copy")
	}

	for i := range MaxHistory + 10 {
		s.AddHistory(fmt.Sprintf("cmd-%d", i))
	}
	if n := len(s.History()); n != MaxHistory {
		t.Fatalf("kept %d commands, want %d", n, MaxHistory)
	}

	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s2.History(), s.History(); len(got) != len(want) || got[len(got)-1] != want[len(want)-1] {
		t.Fatal("history not persisted")
	}
}

func TestDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	s, _ := Open(path)
	if _, _, ok := s.Default(); ok {
		t.Fatal("empty store should have no default")
	}

	s.SetDefault("prod", "") // all namespaces
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if ctx, ns, ok := s2.Default(); !ok || ctx != "prod" || ns != "" {
		t.Fatalf("default = %q %q %v", ctx, ns, ok)
	}

	s2.ClearDefault()
	if err := s2.Save(); err != nil {
		t.Fatal(err)
	}
	s3, _ := Open(path)
	if _, _, ok := s3.Default(); ok {
		t.Fatal("cleared default should not come back")
	}
}
