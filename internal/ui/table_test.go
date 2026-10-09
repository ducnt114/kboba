package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func newTestTable(n, height int) tableModel {
	t := newTableModel()
	t.SetColumns([]tableColumn{{Title: "NAME", Width: 6}, {Title: "STATUS", Width: 8}})
	t.SetWidth(20)
	t.SetHeight(height)
	rows := make([][]string, n)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("pod-%d", i), "Running"}
	}
	t.SetRows(rows, nil)
	return t
}

func TestTableCursorScrolls(t *testing.T) {
	tb := newTestTable(10, 5) // 5 lines - 2 header lines = 3 visible rows

	tb.SetCursor(4)
	if tb.offset != 2 {
		t.Fatalf("cursor 4 → offset %d, want 2 (rows 2-4 visible)", tb.offset)
	}
	tb.SetCursor(1)
	if tb.offset != 1 {
		t.Fatalf("cursor 1 → offset %d, want 1", tb.offset)
	}
	tb.SetCursor(99)
	if tb.cursor != 9 || tb.offset != 7 {
		t.Fatalf("clamped: cursor=%d offset=%d", tb.cursor, tb.offset)
	}

	// Rows disappearing pulls the window back so no blank space remains.
	tb.SetRows(tb.rows[:4], nil)
	if tb.cursor != 3 || tb.offset != 1 {
		t.Fatalf("after shrink: cursor=%d offset=%d", tb.cursor, tb.offset)
	}

	tb.SetRows(nil, nil)
	if tb.cursor != 0 || tb.offset != 0 {
		t.Fatalf("empty: cursor=%d offset=%d", tb.cursor, tb.offset)
	}
}

func TestTableKeys(t *testing.T) {
	tb := newTestTable(10, 5)
	press := func(k tea.KeyMsg) { tb, _ = tb.Update(k) }

	press(tea.KeyMsg{Type: tea.KeyDown})
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if tb.Cursor() != 2 {
		t.Fatalf("cursor = %d", tb.Cursor())
	}
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if tb.Cursor() != 9 {
		t.Fatalf("G → %d", tb.Cursor())
	}
	press(tea.KeyMsg{Type: tea.KeyPgUp})
	if tb.Cursor() != 6 {
		t.Fatalf("pgup → %d", tb.Cursor())
	}
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if tb.Cursor() != 0 {
		t.Fatalf("g → %d", tb.Cursor())
	}
	// "d" is the describe key in the pods view; the table must ignore it.
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if tb.Cursor() != 0 {
		t.Fatalf("d moved the cursor to %d", tb.Cursor())
	}
}

func TestTableView(t *testing.T) {
	tb := newTestTable(10, 5)
	tb.SetColumns([]tableColumn{{Title: "NAME", Width: 4}, {Title: "STATUS", Width: 8}})
	tb.SetCursor(4)

	lines := strings.Split(ansi.Strip(tb.View()), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want header+border+3 rows:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], " NAME ") {
		t.Errorf("header = %q", lines[0])
	}
	// Rows 2-4 are visible; names are truncated to 4 cells with an ellipsis.
	if want := " pod…  Running  "; lines[2] != want {
		t.Errorf("row = %q, want %q", lines[2], want)
	}
	// Every row has the same display width, whatever the content.
	for _, l := range lines[2:] {
		if w := ansi.StringWidth(l); w != (4+2)+(8+2) {
			t.Errorf("row %q has width %d", l, w)
		}
	}
}

func TestFitCell(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"日本語", 4, "日… "}, // wide runes: 2 cells each
		{"x", 0, ""},
	}
	for _, tt := range tests {
		if got := fitCell(tt.in, tt.width); got != tt.want {
			t.Errorf("fitCell(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
}
