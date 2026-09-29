package currency

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/lineofflight/frankfurter/go/internal/seeds"
)

// presence records which optional fields each table entry holds at all. The Money gem keeps an empty string for some
// (GGP has iso_numeric ""), while a currency a patch registers without the field (CMD) has nil, and the v2 API emits
// "" and null accordingly. Info cannot tell the two apart, so this sits beside it.
var presence = sync.OnceValue(func() map[string][2]bool {
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(moneyJSON, &raw); err != nil {
		panic("currency: money.json: " + err.Error())
	}
	out := make(map[string][2]bool, len(raw))
	for id, fields := range raw {
		_, numeric := fields["iso_numeric"]
		_, symbol := fields["symbol"]
		out[id] = [2]bool{numeric, symbol}
	}
	patches, err := seeds.CurrencyPatches()
	if err != nil {
		panic("currency: " + err.Error())
	}
	for _, p := range patches {
		id := strings.ToUpper(p.ISOCode)
		has := out[id]
		has[0] = has[0] || p.ISONumeric != nil
		has[1] = has[1] || p.Symbol != nil
		out[id] = has
	}
	return out
})

// HasISONumeric reports whether the Money gem holds an ISO numeric code for code (possibly empty), rather than nil.
func HasISONumeric(code string) bool { return presence()[strings.ToUpper(code)][0] }

// HasSymbol reports whether the Money gem holds a symbol for code (possibly empty), rather than nil.
func HasSymbol(code string) bool { return presence()[strings.ToUpper(code)][1] }
