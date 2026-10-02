package currency

import (
	"math"
	"reflect"
	"testing"
)

var anchorDate = day(2024, 1, 15)

func find(rows []Blended, quote string) *Blended {
	for i := range rows {
		if rows[i].Quote == quote {
			return &rows[i]
		}
	}
	return nil
}

func TestAnchorPegsUsesPegRateWhenBaseMatches(t *testing.T) {
	rows := []Blended{{Date: anchorDate, Base: "USD", Quote: "AED", Rate: 3.67, Providers: []Contribution{{Key: "ECB"}}}}
	if aed := find(AnchorPegs(rows, "USD"), "AED"); aed.Rate != 3.6725 {
		t.Errorf("AED = %v", aed.Rate)
	}
}

func TestAnchorPegsMarksProvidersExcluded(t *testing.T) {
	rows := []Blended{{Date: anchorDate, Base: "USD", Quote: "AED", Rate: 3.67,
		Providers: []Contribution{{Key: "ECB", Rate: 3.67}}}}
	aed := find(AnchorPegs(rows, "USD"), "AED")
	if want := []Contribution{{Key: "ECB", Rate: 3.67, Excluded: true}}; !reflect.DeepEqual(aed.Providers, want) {
		t.Errorf("providers = %+v", aed.Providers)
	}
	if rows[0].Providers[0].Excluded {
		t.Error("input row mutated")
	}
}

func TestAnchorPegsLeavesRatesBeforePegStart(t *testing.T) {
	rows := []Blended{{Date: day(2014, 6, 2), Base: "USD", Quote: "TMT", Rate: 2.85,
		Providers: []Contribution{{Key: "CBR", Rate: 2.85}}}}
	tmt := find(AnchorPegs(rows, "USD"), "TMT")
	if tmt.Rate != 2.85 || !reflect.DeepEqual(tmt.Providers, []Contribution{{Key: "CBR", Rate: 2.85}}) {
		t.Errorf("TMT = %+v", tmt)
	}
}

func TestAnchorPegsCrossBase(t *testing.T) {
	rows := []Blended{
		{Date: anchorDate, Base: "EUR", Quote: "USD", Rate: 1.10, Providers: []Contribution{{Key: "ECB"}}},
		{Date: anchorDate, Base: "EUR", Quote: "AED", Rate: 4.04, Providers: []Contribution{{Key: "ECB"}}},
	}
	if aed := find(AnchorPegs(rows, "EUR"), "AED"); math.Abs(aed.Rate-1.10*3.6725) > 0.0001 {
		t.Errorf("AED = %v", aed.Rate)
	}
}

func TestAnchorPegsSynthesizesUncoveredCurrencies(t *testing.T) {
	rows := []Blended{{Date: anchorDate, Base: "EUR", Quote: "GBP", Rate: 0.86,
		Providers: []Contribution{{Key: "ECB", Rate: 0.86}}}}
	fkp := find(AnchorPegs(rows, "EUR"), "FKP")
	if fkp == nil {
		t.Fatal("no FKP row")
	}
	if math.Abs(fkp.Rate-0.86) > 0.0001 || fkp.Providers != nil {
		t.Errorf("FKP = %+v", fkp)
	}
}

func TestAnchorPegsSkipsSynthesisBeforePegStart(t *testing.T) {
	rows := []Blended{{Date: day(1900, 1, 1), Base: "EUR", Quote: "GBP", Rate: 0.86,
		Providers: []Contribution{{Key: "ECB"}}}}
	if fkp := find(AnchorPegs(rows, "EUR"), "FKP"); fkp != nil {
		t.Errorf("FKP = %+v", fkp)
	}
}

func TestAnchorPegsEmpty(t *testing.T) {
	if got := AnchorPegs(nil, "EUR"); got == nil || len(got) != 0 {
		t.Errorf("got %#v", got)
	}
}
