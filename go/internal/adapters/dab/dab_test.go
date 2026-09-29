package dab

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/golden"
	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

var day = adapter.Date(2026, 5, 20)

func fetch(t *testing.T, after, upto time.Time) []adapter.Rate {
	t.Helper()
	a := New(vcrtest.Client(t, "dab", vcrtest.MatchOn(vcrtest.Method, vcrtest.URI)))
	rates, err := a.Fetch(context.Background(), after, upto)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func mustParse(t *testing.T, html string) []adapter.Rate {
	t.Helper()
	rates, err := parse([]byte(html), day)
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestFetchDateRange(t *testing.T) {
	if rates := fetch(t, adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20)); len(rates) == 0 {
		t.Fatal("no rates")
	}
}

func TestFetchMultipleCurrenciesPerDate(t *testing.T) {
	rates := fetch(t, day, day)
	for _, r := range rates {
		if !r.Date.Equal(day) {
			t.Errorf("date = %v, want %v", r.Date, day)
		}
	}
	if len(rates) <= 5 {
		t.Errorf("got %d rates, want more than 5", len(rates))
	}
}

func TestFetchAFNQuoteForeignBase(t *testing.T) {
	rates := fetch(t, day, day)
	if len(rates) == 0 {
		t.Fatal("no rates")
	}
	var bases []string
	for _, r := range rates {
		if r.Quote != "AFN" {
			t.Errorf("quote = %q, want AFN", r.Quote)
		}
		bases = append(bases, r.Base)
	}
	if !slices.Contains(bases, "USD") {
		t.Errorf("bases %v lack USD", bases)
	}
}

func TestParseDailyTableTransferMid(t *testing.T) {
	rates := mustParse(t, `<div class="table-responsive">
  <table class="table table-striped">
    <thead>
      <tr>
        <th>Currency</th>
        <th>Cash (Sell)</th>
        <th>Cash (Buy)</th>
        <th>Transfer (Sell)</th>
        <th>Transfer (Buy)</th>
      </tr>
    </thead>
    <tbody>
      <tr>
        <td>USD$</td>
        <td>63.7940</td>
        <td>63.5940</td>
        <td>63.7440</td>
        <td>63.6440</td>
      </tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "USD" || r.Quote != "AFN" {
		t.Errorf("pair = %s/%s, want USD/AFN", r.Base, r.Quote)
	}
	if math.Abs(r.Rate-63.694) > 0.0001 {
		t.Errorf("rate = %v, want 63.694", r.Rate)
	}
}

func TestParseMapsLabelsToISOCodes(t *testing.T) {
	rates := mustParse(t, `<div class="table-responsive">
  <table class="table table-striped">
    <tbody>
      <tr><td>EURO€</td><td>74</td><td>73</td><td>73.5</td><td>73.0</td></tr>
      <tr><td>POUND£</td><td>86</td><td>85</td><td>85.5</td><td>85.0</td></tr>
      <tr><td>SWISS₣</td><td>82</td><td>81</td><td>81.5</td><td>81.0</td></tr>
      <tr><td>INDIAN Rs.</td><td>0.76</td><td>0.74</td><td>0.755</td><td>0.745</td></tr>
      <tr><td>PAKISTAN Rs.</td><td>0.23</td><td>0.21</td><td>0.225</td><td>0.215</td></tr>
      <tr><td>CNY¥</td><td>9.9</td><td>9.5</td><td>9.8</td><td>9.6</td></tr>
      <tr><td>UAE DIRHAM</td><td>17.3</td><td>17.1</td><td>17.25</td><td>17.15</td></tr>
      <tr><td>SAUDI RIYAL</td><td>16.7</td><td>16.6</td><td>16.68</td><td>16.62</td></tr>
    </tbody>
  </table>
</div>`)
	var codes []string
	for _, r := range rates {
		codes = append(codes, r.Base)
	}
	if want := []string{"EUR", "GBP", "CHF", "INR", "PKR", "CNY", "AED", "SAR"}; !slices.Equal(codes, want) {
		t.Errorf("codes = %v, want %v", codes, want)
	}
}

func TestParseTomanToIRR(t *testing.T) {
	rates := mustParse(t, `<div class="table-responsive">
  <table class="table table-striped">
    <tbody>
      <tr>
        <td>IRAN Toman</td>
        <td>0.0009</td>
        <td>0.0003</td>
        <td>0.0008</td>
        <td>0.0004</td>
      </tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	r := rates[0]
	if r.Base != "IRR" || r.Quote != "AFN" {
		t.Errorf("pair = %s/%s, want IRR/AFN", r.Base, r.Quote)
	}
	// Transfer mid for Toman: (0.0008 + 0.0004) / 2 = 0.0006; per IRR that is 0.00006.
	if math.Abs(r.Rate-0.00006) > 1e-7 {
		t.Errorf("rate = %v, want 0.00006", r.Rate)
	}
}

func TestParseIgnoresAverageRatesTable(t *testing.T) {
	rates := mustParse(t, `<div class="table-responsive">
  <table class="table table-striped">
    <tbody>
      <tr><td>USD$</td><td>63.79</td><td>63.59</td><td>63.74</td><td>63.64</td></tr>
    </tbody>
  </table>
</div>
<div class="table-responsive">
  <table class="table table-striped">
    <tbody>
      <tr><td>USD$</td><td>64.83</td><td>64.63</td><td>64.78</td><td>64.68</td></tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 1 {
		t.Fatalf("got %d rates, want 1", len(rates))
	}
	if math.Abs(rates[0].Rate-63.69) > 0.01 {
		t.Errorf("rate = %v, want 63.69", rates[0].Rate)
	}
}

func TestParseNoResults(t *testing.T) {
	rates, err := parse([]byte(`<div class="messages">There were no results.</div>`), adapter.Date(2021, 1, 15))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 0 {
		t.Errorf("got %d rates, want none", len(rates))
	}
}

func TestParseIgnoresEmptyCells(t *testing.T) {
	rates := mustParse(t, `<div class="table-responsive">
  <table class="table table-striped">
    <tbody>
      <tr><td></td><td></td><td></td><td></td><td></td></tr>
      <tr><td>USD$</td><td>63.79</td><td>63.59</td><td>63.74</td><td>63.64</td></tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("rates = %+v, want one USD row", rates)
	}
}

func TestParseIgnoresUnknownLabels(t *testing.T) {
	rates := mustParse(t, `<div class="table-responsive">
  <table class="table table-striped">
    <tbody>
      <tr><td>MARTIAN credit</td><td>1</td><td>1</td><td>1</td><td>1</td></tr>
      <tr><td>USD$</td><td>63.79</td><td>63.59</td><td>63.74</td><td>63.64</td></tr>
    </tbody>
  </table>
</div>`)
	if len(rates) != 1 || rates[0].Base != "USD" {
		t.Errorf("rates = %+v, want one USD row", rates)
	}
}

func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 5, 18), adapter.Date(2026, 5, 20))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
