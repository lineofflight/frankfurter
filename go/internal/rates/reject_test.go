package rates

import (
	"testing"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/currency"
)

func TestRejectDropsPrematureCurrenciesWithoutPredecessor(t *testing.T) {
	entry, _ := currency.FindNascent("EUR")
	entry.Predecessor = ""
	stub := func(code string) (currency.Nascent, bool) { return entry, true }
	records := []adapter.Rate{
		{Date: adapter.Date(1998, 12, 31), Base: "EUR", Quote: "USD", Rate: 1.1},
		{Date: adapter.Date(1998, 12, 31), Base: "USD", Quote: "EUR", Rate: 0.9},
	}
	if got := reject(records, 0, time.Now(), stub); len(got) != 0 {
		t.Errorf("kept %+v", got)
	}
}
