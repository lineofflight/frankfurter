package rates

import (
	"math"
	"strconv"
	"strings"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

// Digits is the ingest precision, in significant digits.
//
// Adapters routinely synthesise figures no source published: buy/sell midpoints, per-unit rescaling, cross rates.
// Float arithmetic leaves noise in the low digits ((181.5264 + 181.76) / 2 = 181.64319999999998). Blended output
// rounds anyway, but single-provider responses echo stored digits verbatim, so the noise must go before storage. No
// reference rate is published to more than about ten significant digits and a double carries fifteen to seventeen;
// twelve sits between the deepest real digit and the noise floor.
//
// Rollups and blends are excluded on purpose: their trailing digits are synthetic by construction and both round on
// output.
const Digits = 12

// Normalize rounds a rate to Digits significant digits. NaN and infinities pass through.
func Normalize(rate float64) float64 {
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return rate
	}
	return formatRound(rate, true, Digits)
}

// PrecisionSQL is Normalize in SQLite, for rows stored before the policy existed.
func PrecisionSQL(expr string) string {
	return "CAST(printf('%.12g', " + expr + ") AS REAL)"
}

// Midpoint is the exact decimal mean of bid and ask, or nil unless both are published.
func Midpoint(bid, ask *float64) *float64 {
	if bid == nil || ask == nil {
		return nil
	}
	return adapter.Float(adapter.Midpoint(*bid, *ask))
}

// Components are the columns a rate row is stored through. The rates table resolves rate from them when selected
// (mid, else the rounded midpoint of bid and ask), so the adapter's Rate is never stored.
type Components struct {
	Mid, Bid, Ask *float64
}

// ComponentsOf is RateComponents.attributes. A row with any published component keeps its published mid, even nil, so
// a derived midpoint never masquerades as a published one; a row without components stores its rate as mid. The
// stored mid is normalised to Digits.
//
// Ruby tells the two apart by whether the record has a :mid key; adapter.Rate has no such key, so a row whose bid,
// ask and mid are all nil counts as having no components.
func ComponentsOf(r adapter.Rate) Components {
	mid := r.Mid
	if r.Bid == nil && r.Ask == nil && r.Mid == nil {
		mid = adapter.Float(r.Rate)
	}
	if mid != nil {
		mid = adapter.Float(Normalize(*mid))
	}
	return Components{Mid: mid, Bid: r.Bid, Ask: r.Ask}
}

// Round is Roundable#round: most pairs quote to four decimals; below 1 to five or six, above 20 to three, above 80 to
// two, above 5000 to none (https://en.wikipedia.org/wiki/Exchange_rate#Quotations).
func Round(v float64) float64 {
	var decimals int
	switch {
	case v > 5000:
		return math.Round(v)
	case v > 80:
		decimals = 2
	case v > 20:
		decimals = 3
	case v > 1:
		decimals = 4
	case v > 0.0001:
		decimals = 5
	default:
		decimals = 6
	}
	return formatRound(v, false, decimals)
}

// formatRound is Float(format("%.<n>g" or "%.<n>f", v)) as Ruby computes it: to n significant digits when sig is
// set, else to n decimals.
//
// Ruby does not round the exact binary value, as strconv does. It rounds the shortest decimal that round-trips to v,
// half to even, so format("%.2f", 214.415) is 214.42 although the double lies just below 214.415, and
// format("%.2f", 643.965) is 643.96 although it lies just above. Checked against Ruby on 55,000 values; the only
// misses were 16- and 17-digit values within about 2e-4 of a tie at the twelfth significant digit, where Ruby also
// breaks the tie to even.
func formatRound(v float64, sig bool, n int) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(math.Abs(v), 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e, _ := strconv.Atoi(exp)
	point := e + 1 // digits before the decimal point; v = 0.digits × 10^point
	keep := n
	if !sig {
		keep = point + n
	}
	if keep >= len(digits) {
		return v
	}
	var q uint64
	if keep >= 0 {
		d, _ := strconv.ParseUint(digits, 10, 64)
		unit := uint64(math.Pow10(len(digits) - keep))
		q = d / unit
		if r := d % unit; 2*r > unit || 2*r == unit && q%2 == 1 {
			q++
		}
	}
	out, _ := strconv.ParseFloat(strconv.FormatUint(q, 10)+"e"+strconv.Itoa(point-keep), 64)
	return math.Copysign(out, v)
}
