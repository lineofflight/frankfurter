package currency

import (
	"regexp"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

var (
	isoCode = regexp.MustCompile(`\A[A-Z]{3}\z`)
	httpURL = regexp.MustCompile(`\Ahttps?://`)
)

// Ruby's "returns a frozen array" and "parses every date as a Date" hold by
// construction: the registries are unexported slices behind accessors, and the
// dates are time.Time.

func TestDefunctLoadsAllEntries(t *testing.T) {
	if len(DefunctCurrencies()) == 0 {
		t.Fatal("no defunct currencies")
	}
	for _, e := range DefunctCurrencies() {
		if e.TerminalDate.IsZero() {
			t.Errorf("%s has no terminal date", e.ISOCode)
		}
	}
}

func TestDefunctRequiresCodeDateAndSource(t *testing.T) {
	for _, e := range DefunctCurrencies() {
		if !isoCode.MatchString(e.ISOCode) || !httpURL.MatchString(e.Source) {
			t.Errorf("bad entry %+v", e)
		}
	}
}

func TestDefunctCoversKnownCodes(t *testing.T) {
	for _, code := range []string{"ATS", "BEF", "BGN", "BYR", "CUC", "DEM", "ECS", "EEK", "ESP", "FRF", "HRK", "IEP",
		"ITL", "NLG", "PTE", "SLL", "STD", "VEF", "ZMK"} {
		if _, ok := FindDefunct(code); !ok {
			t.Errorf("missing %s", code)
		}
	}
}

func TestDefunctLooksUpByCode(t *testing.T) {
	e, ok := FindDefunct("BYR")
	if !ok || !e.TerminalDate.Equal(day(2016, 7, 1)) || e.Successor != "BYN" || e.Ratio != 10000 {
		t.Errorf("BYR = %+v", e)
	}
}

func TestDefunctRetiresSucreAfterWithdrawalPeriod(t *testing.T) {
	e, _ := FindDefunct("ECS")
	if !e.TerminalDate.Equal(day(2000, 9, 9)) || e.Successor != "USD" || e.Ratio != 25000 {
		t.Errorf("ECS = %+v", e)
	}
}

func TestDefunctUnknownCode(t *testing.T) {
	if _, ok := FindDefunct("USD"); ok {
		t.Error("USD is not defunct")
	}
}

func TestExpired(t *testing.T) {
	for _, c := range []struct {
		name string
		code string
		date time.Time
		want bool
	}{
		{"on the terminal date", "BYR", day(2016, 7, 1), true},
		{"after the terminal date", "BYR", day(2017, 1, 1), true},
		{"before the terminal date", "BYR", day(2016, 6, 30), false},
		{"a code not in the table", "USD", day(2030, 1, 1), false},
	} {
		if got := Expired(c.code, c.date); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestNascentLoadsAllEntries(t *testing.T) {
	if len(NascentCurrencies()) == 0 {
		t.Fatal("no nascent currencies")
	}
	for _, e := range NascentCurrencies() {
		if e.InceptionDate.IsZero() || !isoCode.MatchString(e.ISOCode) || !httpURL.MatchString(e.Source) {
			t.Errorf("bad entry %+v", e)
		}
	}
}

func TestNascentCoversEuro(t *testing.T) {
	e, ok := FindNascent("EUR")
	if !ok || !e.InceptionDate.Equal(day(1999, 1, 1)) || e.Predecessor != "XEU" {
		t.Errorf("EUR = %+v", e)
	}
	if _, ok := FindNascent("USD"); ok {
		t.Error("USD is not nascent")
	}
}

func TestPremature(t *testing.T) {
	for _, c := range []struct {
		name string
		code string
		date time.Time
		want bool
	}{
		{"before the inception date", "EUR", day(1998, 12, 31), true},
		{"on the inception date", "EUR", day(1999, 1, 1), false},
		{"after the inception date", "EUR", day(2020, 1, 1), false},
		{"a code not in the table", "USD", day(1900, 1, 1), false},
	} {
		if got := Premature(c.code, c.date); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestPegsLoad(t *testing.T) {
	if len(Pegs()) == 0 {
		t.Fatal("no pegs")
	}
}

func TestPegAttributes(t *testing.T) {
	bmd, ok := FindPeg("BMD")
	if !ok || bmd.Base != "USD" || bmd.Rate != 1.0 || !bmd.Since.Equal(day(1972, 2, 6)) ||
		bmd.Authority != "Bermuda Monetary Authority" || !regexp.MustCompile("wikipedia").MatchString(bmd.Source) {
		t.Errorf("BMD = %+v", bmd)
	}
}

func TestPegNotFound(t *testing.T) {
	if _, ok := FindPeg("EUR"); ok {
		t.Error("EUR is not pegged")
	}
}

func TestPegNonUnitRate(t *testing.T) {
	if ang, _ := FindPeg("ANG"); ang.Rate != 1.79 {
		t.Errorf("ANG rate = %v", ang.Rate)
	}
}
