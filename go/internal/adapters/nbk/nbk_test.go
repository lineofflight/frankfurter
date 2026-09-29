package nbk

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

func fetch(t *testing.T) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "nbk", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host)))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchWithDateRange(t *testing.T) {
	if rates := fetch(t); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t)
	first := rates[0].Date
	n := 0
	for _, r := range rates {
		if r.Date.Equal(first) {
			n++
		}
	}
	if n <= 1 {
		t.Errorf("got %d rates on %v, want several", n, first)
	}
}

func xmlWith(item string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rates>
  <date>01.04.2026</date>
  <item>` + item + `</item>
</rates>`)
}

func TestParse(t *testing.T) {
	rates, err := parse(xmlWith(`
    <fullname>ДОЛЛАР США</fullname>
    <title>USD</title>
    <description>460.37</description>
    <quant>1</quant>
    <index>DOWN</index>
    <change>-5.96</change>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "KZT" {
		t.Errorf("got %s/%s, want USD/KZT", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-460.37) > 0.01 {
		t.Errorf("rate = %v, want 460.37", r.Rate)
	}
	if !r.Date.Equal(adapter.Date(2026, 4, 1)) {
		t.Errorf("date = %v, want 2026-04-01", r.Date)
	}
}

func TestParseNormalizesRateByQuantity(t *testing.T) {
	rates, err := parse(xmlWith(`
    <fullname>ВОНА ЮЖНО-КОРЕЙСКАЯ</fullname>
    <title>KRW</title>
    <description>30.69</description>
    <quant>100</quant>
    <index>DOWN</index>
    <change>-0.32</change>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	if math.Abs(rates[0].Rate-0.3069) > 0.0001 {
		t.Errorf("rate = %v, want 0.3069", rates[0].Rate)
	}
}

func TestParseSkips(t *testing.T) {
	tests := map[string]string{
		"zero rates": `
    <fullname>ДОЛЛАР США</fullname>
    <title>USD</title>
    <description>0.0</description>
    <quant>1</quant>
    <index>DOWN</index>
    <change>0</change>`,
		"invalid currency codes": `
    <fullname>Invalid</fullname>
    <title>XX</title>
    <description>1.5</description>
    <quant>1</quant>
    <index>DOWN</index>
    <change>0</change>`,
	}
	for name, item := range tests {
		t.Run(name, func(t *testing.T) {
			rates, err := parse(xmlWith(item))
			if err != nil {
				t.Fatal(err)
			}
			if len(rates) != 0 {
				t.Errorf("got %d rates, want none", len(rates))
			}
		})
	}
}

func TestParseRaisesWhenDateMissing(t *testing.T) {
	_, err := parse([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rates>
</rates>`))
	if err == nil || !strings.Contains(err.Error(), "<date> missing") {
		t.Errorf("err = %v, want <date> missing", err)
	}
}

func TestToF(t *testing.T) {
	for s, want := range map[string]float64{"460.37": 460.37, " 1": 1, "": 0, "abc": 0, "12abc": 12} {
		if got := toF(s); got != want {
			t.Errorf("toF(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 4, 1), adapter.Date(2026, 4, 3))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
