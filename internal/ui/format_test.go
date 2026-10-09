package ui

import (
	"testing"
	"time"
)

func TestFormatAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{0, "0s"},
		{42 * time.Second, "42s"},
		{5 * time.Minute, "5m"},
		{5*time.Minute + 10*time.Second, "5m10s"},
		{45 * time.Minute, "45m"},
		{3*time.Hour + 4*time.Minute, "3h4m"},
		{15 * time.Hour, "15h"},
		{2*24*time.Hour + 5*time.Hour, "2d5h"},
		{120 * 24 * time.Hour, "120d"},
		{(365 + 40) * 24 * time.Hour, "1y40d"},
	}
	for _, tt := range tests {
		if got := formatAge(tt.d); got != tt.want {
			t.Errorf("formatAge(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestReceiveBatch(t *testing.T) {
	ch := make(chan int, 10)
	for i := range 5 {
		ch <- i
	}

	got, ok := receiveBatch(ch, 3)
	if !ok || len(got) != 3 {
		t.Fatalf("first batch = %v, %v", got, ok)
	}
	got, ok = receiveBatch(ch, 3)
	if !ok || len(got) != 2 {
		t.Fatalf("second batch = %v, %v", got, ok)
	}

	ch <- 99
	close(ch)
	got, ok = receiveBatch(ch, 3)
	if !ok || len(got) != 1 || got[0] != 99 {
		t.Fatalf("batch before close = %v, %v", got, ok)
	}
	if _, ok = receiveBatch(ch, 3); ok {
		t.Fatal("expected ok=false on closed channel")
	}
}

func TestFormatUsage(t *testing.T) {
	if got := formatCPU(250); got != "250m" {
		t.Errorf("formatCPU = %q", got)
	}
	for bytes, want := range map[int64]string{
		0:                 "0Mi",
		128 * 1024 * 1024: "128Mi",
		1536 * 1024:       "2Mi", // 1.5Mi rounds up
		2 << 30:           "2048Mi",
	} {
		if got := formatMemory(bytes); got != want {
			t.Errorf("formatMemory(%d) = %q, want %q", bytes, got, want)
		}
	}
}
