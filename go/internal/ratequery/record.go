package ratequery

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Record is one row of a v2 rates response: Rate units of Quote per one Base, observed on Date.
type Record struct {
	Date  string
	Base  string
	Quote string
	Rate  Number

	// Providers lists the contributors when expand=providers asked for them. HasProviders tells an empty list from
	// none, as Ruby tells a record with providers: [] from one without the key.
	Providers    []Contribution
	HasProviders bool
}

// Contribution is one provider's part in a record.
type Contribution struct {
	Key      string
	Date     string
	Rate     Number
	Excluded bool
}

// Number is a rate as Ruby holds it: a Float, or an Integer where Roundable rounds a value over 5000 to whole units.
// The distinction shows only in CSV, where Ruby prints 1.0 but 12345.
type Number struct {
	Value float64
	Int   bool
}

// Float is a Ruby Float.
func Float(v float64) Number { return Number{Value: v} }

// MarshalJSON writes the number as JSON.
func (n Number) MarshalJSON() ([]byte, error) {
	if n.Int {
		return []byte(strconv.FormatFloat(n.Value, 'f', 0, 64)), nil
	}
	return json.Marshal(n.Value)
}

// String is Ruby's Float#to_s or Integer#to_s.
func (n Number) String() string {
	if n.Int {
		return strconv.FormatFloat(n.Value, 'f', 0, 64)
	}
	return RubyFloat(n.Value)
}

// RubyFloat formats v as Ruby's Float#to_s: the shortest digits that round-trip, in fixed notation (always with a
// fractional part) for magnitudes from 1e-4 up to 1e16, in exponent notation (1.0e-05, 1.2e+16) otherwise.
func RubyFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0:
		if math.Signbit(v) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	e := strconv.FormatFloat(v, 'e', -1, 64) // d.ddde±XX
	mantissa, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	decpt := x + 1 // v = 0.digits × 10^decpt

	switch {
	case decpt > 0 && decpt <= 15:
		if len(digits) <= decpt {
			return sign + digits + strings.Repeat("0", decpt-len(digits)) + ".0"
		}
		return sign + digits[:decpt] + "." + digits[decpt:]
	case decpt <= 0 && decpt > -4:
		return sign + "0." + strings.Repeat("0", -decpt) + digits
	}
	frac := digits[1:]
	if frac == "" {
		frac = "0"
	}
	expSign := "+"
	if decpt-1 < 0 {
		expSign = "-"
	}
	n := decpt - 1
	if n < 0 {
		n = -n
	}
	es := strconv.Itoa(n)
	if len(es) < 2 {
		es = "0" + es
	}
	return sign + digits[:1] + "." + frac + "e" + expSign + es
}

// MarshalJSON writes the record with Ruby's key order: date, base, quote, rate, then providers when present.
func (r Record) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"date":`)
	writeString(&b, r.Date)
	b.WriteString(`,"base":`)
	writeString(&b, r.Base)
	b.WriteString(`,"quote":`)
	writeString(&b, r.Quote)
	b.WriteString(`,"rate":`)
	if err := writeNumber(&b, r.Rate); err != nil {
		return nil, err
	}
	if r.HasProviders {
		b.WriteString(`,"providers":[`)
		for i, p := range r.Providers {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"key":`)
			writeString(&b, p.Key)
			b.WriteString(`,"date":`)
			writeString(&b, p.Date)
			b.WriteString(`,"rate":`)
			if err := writeNumber(&b, p.Rate); err != nil {
				return nil, err
			}
			if p.Excluded {
				b.WriteString(`,"excluded":true`)
			}
			b.WriteByte('}')
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func writeString(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	b.Truncate(b.Len() - 1) // Encode's newline
}

func writeNumber(b *bytes.Buffer, n Number) error {
	data, err := n.MarshalJSON()
	if err != nil {
		return err
	}
	b.Write(data)
	return nil
}
