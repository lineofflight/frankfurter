package currency

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

// testdata/patched.json is Ruby's table after boot applies the patches (go/scripts/dump_money.rb run after requiring
// boot.rb).
func TestTableMatchesPatchedRubyTable(t *testing.T) {
	b, err := os.ReadFile("testdata/patched.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]Info
	var raw map[string]struct {
		ISOCode       string `json:"iso_code"`
		Name          string `json:"name"`
		Symbol        string `json:"symbol"`
		ISONumeric    string `json:"iso_numeric"`
		SubunitToUnit int    `json:"subunit_to_unit"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	want = map[string]Info{}
	for k, v := range raw {
		want[k] = Info(v)
	}
	got := table()
	if len(got) != len(want) {
		t.Errorf("got %d codes, want %d", len(got), len(want))
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %+v, want %+v", k, got[k], w)
		}
	}
}

func TestAppliesAllPatches(t *testing.T) {
	patches, err := seeds.CurrencyPatches()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range patches {
		info, ok := Find(p.ISOCode)
		if !ok {
			t.Errorf("%s not registered", p.ISOCode)
			continue
		}
		if info.Name != *p.Name {
			t.Errorf("%s name = %q, want %q", p.ISOCode, info.Name, *p.Name)
		}
	}
}

func TestRegistersEcuadorianSucre(t *testing.T) {
	info, _ := Find("ECS")
	if info.Name != "Ecuadorian Sucre" || info.ISONumeric != "218" || info.SubunitToUnit != 100 {
		t.Errorf("ECS = %+v", info)
	}
}

func TestRegistersCOMESADollarWithoutISONumeric(t *testing.T) {
	info, _ := Find("CMD")
	if info.Name != "COMESA Dollar" || info.ISONumeric != "" {
		t.Errorf("CMD = %+v", info)
	}
}

func TestRecognizesRegisteredAliases(t *testing.T) {
	info, ok := Find("GHC")
	if !ok || info.ISOCode != "GHS" {
		t.Errorf("GHC = %+v, %v", info, ok)
	}
	if _, ok := Find("ghc"); !ok {
		t.Error("lookup is not case-insensitive")
	}
}
