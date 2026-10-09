package state

import (
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
