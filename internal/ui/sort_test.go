package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"

	"github.com/ducnt114/kboba/internal/k8s"
)

func TestNaturalLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"9", "10", true},
		{"10", "9", false},
		{"pod-9", "pod-10", true},
		{"1/3", "2/3", true},
		{"0/1", "1/1", true},
		{"Apple", "banana", true}, // case-insensitive
		{"abc", "abcd", true},     // prefix first
		{"007", "7", false},       // equal numbers: not less
		{"7", "007", false},
		{"Running", "Pending", false},
		{"99999999999999999999", "100000000000000000000", true}, // no overflow
	}
	for _, tt := range tests {
		if got := naturalLess(tt.a, tt.b); got != tt.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}

	names := []string{"web-10", "web-2", "Web-1", "api"}
	slices.SortFunc(names, func(a, b string) int {
		switch {
		case naturalLess(a, b):
			return -1
		case naturalLess(b, a):
			return 1
		}
		return 0
	})
	if want := []string{"api", "Web-1", "web-2", "web-10"}; !slices.Equal(names, want) {
		t.Fatalf("sorted = %v, want %v", names, want)
	}
}

func TestSortEntries(t *testing.T) {
	now := time.Now()
	mk := func(key, restarts string, age time.Duration) rowEntry {
		return rowEntry{key: key, res: k8s.Resource{Created: now.Add(-age)}, row: table.Row{key, restarts}}
	}
	entries := func() []rowEntry {
		return []rowEntry{
			mk("ns/c", "10", time.Hour),
			mk("ns/a", "9", time.Minute),
			mk("ns/b", "10", 2*time.Hour),
		}
	}
	keys := func(es []rowEntry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.key)
		}
		return out
	}

	tests := []struct {
		name  string
		col   int
		byAge bool
		desc  bool
		want  []string
	}{
		{"default order is by key", -1, false, false, []string{"ns/a", "ns/b", "ns/c"}},
		{"default reversed", -1, false, true, []string{"ns/c", "ns/b", "ns/a"}},
		{"numeric column, ties by key", 1, false, false, []string{"ns/a", "ns/b", "ns/c"}},
		{"numeric column reversed, ties still by key", 1, false, true, []string{"ns/b", "ns/c", "ns/a"}},
		{"age: youngest first", -1, true, false, []string{"ns/a", "ns/c", "ns/b"}},
		{"age reversed: oldest first", -1, true, true, []string{"ns/b", "ns/c", "ns/a"}},
	}
	for _, tt := range tests {
		es := entries()
		sortEntries(es, tt.col, tt.byAge, tt.desc)
		if got := keys(es); !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestNextSortColumn(t *testing.T) {
	titles := []string{"NAME", "STATUS", "AGE"}
	steps := []string{"NAME", "STATUS", "AGE", "", "NAME"}
	cur := ""
	for _, want := range steps {
		cur = nextSortColumn(titles, cur)
		if cur != want {
			t.Fatalf("got %q, want %q", cur, want)
		}
	}
	// A column that no longer exists (e.g. NAMESPACE after leaving "all")
	// restarts the cycle.
	if got := nextSortColumn(titles, "NAMESPACE"); got != "NAME" {
		t.Fatalf("got %q", got)
	}
}
