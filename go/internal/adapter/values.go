package adapter

import (
	"fmt"
	"math/big"
	"strconv"
	"time"
)

// Date returns the given calendar day as UTC midnight, the representation of every date in the port.
func Date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// ParseDate parses s with the first layout that fits. Ruby's Date.parse guesses; Go needs the layouts a source
// actually uses spelled out, e.g. ParseDate(s, "2 January 2006", "02-Jan-06").
func ParseDate(s string, layouts ...string) (time.Time, error) {
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}

// Window keeps the rates dated after `after` (exclusive) through `upto` (inclusive), treating a zero bound as open.
// It filters in place.
func Window(rates []Rate, after, upto time.Time) []Rate {
	kept := rates[:0]
	for _, r := range rates {
		if (!after.IsZero() && !r.Date.After(after)) || (!upto.IsZero() && r.Date.After(upto)) {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// Predecessor names the code a source's rows carry under before a redenomination took effect in its values.
type Predecessor struct {
	Code    string    // the predecessor's ISO code
	Cutover time.Time // first date the source's values are in the successor unit
}

// HistoricalCode relabels code with its predecessor for rows dated before the cutover. Sources often label a
// redenominated currency's whole history with its current code without restating the values, so the old rows carry the
// predecessor's magnitudes under the successor's code. An adapter that sees this declares a map from current code to
// Predecessor and passes every row's code through here.
func HistoricalCode(predecessors map[string]Predecessor, code string, date time.Time) string {
	if p, ok := predecessors[code]; ok && date.Before(p.Cutover) {
		return p.Code
	}
	return code
}

// Float returns a pointer to v, for Rate's optional components.
func Float(v float64) *float64 { return &v }

// Midpoint is the exact decimal mean of two published prices, rounded once to the nearest float. Float arithmetic
// leaves noise ((181.5264 + 181.76) / 2 = 181.64319999999998) because the operands are already inexact; halving a
// terminating decimal always terminates, so decimal arithmetic gives the mid exactly.
func Midpoint(bid, ask float64) float64 {
	sum := new(big.Rat).Add(decimal(bid), decimal(ask))
	return ratFloat(sum.Quo(sum, big.NewRat(2, 1)))
}

// PerUnit divides a price quoted per `unit` foreign units (per 100 JPY, say) down to one unit, in decimal so that
// 744.92 / 100 is 7.4492 and not 7.449199999999999.
func PerUnit(price, unit float64) float64 {
	if unit == 1 {
		return price
	}
	return ratFloat(new(big.Rat).Quo(decimal(price), decimal(unit)))
}

// decimal reads f as the shortest decimal that round-trips, as Ruby's BigDecimal(f.to_s) does. A value parsed from
// published text of up to 15 significant digits comes back as exactly that text.
func decimal(f float64) *big.Rat {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(f, 'g', -1, 64))
	if !ok {
		panic(fmt.Sprintf("adapter: not a finite number: %v", f))
	}
	return r
}

func ratFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}
