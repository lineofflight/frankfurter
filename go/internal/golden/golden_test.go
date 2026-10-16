package golden_test

import (
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
)

func row(date, base string, rate float64) map[string]any {
	return map[string]any{"date": date, "base": base, "quote": "USD", "rate": rate}
}

func TestDiff(t *testing.T) {
	want := []map[string]any{row("2026-03-17", "EUR", 1.1), row("2026-03-17", "GBP", 1.3)}

	tests := []struct {
		name     string
		got      []map[string]any
		contains string
	}{
		{"identical in any order", []map[string]any{row("2026-03-17", "GBP", 1.3), row("2026-03-17", "EUR", 1.1)}, ""},
		{"within tolerance", []map[string]any{row("2026-03-17", "EUR", 1.1*(1+1e-12)), row("2026-03-17", "GBP", 1.3)}, ""},
		{"rate off", []map[string]any{row("2026-03-17", "EUR", 1.2), row("2026-03-17", "GBP", 1.3)}, "rate want 1.1, got 1.2"},
		{"missing row", []map[string]any{row("2026-03-17", "EUR", 1.1)}, "missing:"},
		{"extra row", append([]map[string]any{row("2026-03-18", "EUR", 1.1)}, want...), "extra:"},
		{"field differs", []map[string]any{row("2026-03-17", "EUR", 1.1), row("2026-03-16", "GBP", 1.3)}, "missing:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := golden.Diff(want, tt.got)
			if tt.contains == "" && diff != "" {
				t.Errorf("unexpected diff:\n%s", diff)
			}
			if tt.contains != "" && !strings.Contains(diff, tt.contains) {
				t.Errorf("diff lacks %q:\n%s", tt.contains, diff)
			}
		})
	}
}

func TestRowCarriesPublishedComponents(t *testing.T) {
	r := adapter.Rate{Date: adapter.Date(2026, 3, 17), Base: "USD", Quote: "IDR", Rate: 2, Bid: adapter.Float(1), Ask: adapter.Float(3)}
	got := golden.Row(r)
	if got["date"] != "2026-03-17" || got["bid"] != 1.0 || got["ask"] != 3.0 {
		t.Errorf("Row = %v", got)
	}
	if _, ok := got["mid"]; ok {
		t.Error("Row includes an unpublished mid")
	}
}
