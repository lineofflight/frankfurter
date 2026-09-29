package ratequery

import (
	"encoding/json"
	"testing"
)

// Ruby's Float#to_s, as printed by Ruby 4.0 for these values.
func TestRubyFloat(t *testing.T) {
	for v, want := range map[float64]string{
		1.0: "1.0", 0.0001: "0.0001", 0.00001: "1.0e-05", 1.2e-05: "1.2e-05", 1e15: "1.0e+15", 1e16: "1.0e+16",
		123456789.123: "123456789.123", -0.5: "-0.5", 2.345678: "2.345678", 100.0: "100.0", 1e14: "100000000000000.0",
		99999999999999.0: "99999999999999.0", 999999999999999.0: "999999999999999.0",
		123456789012345.6: "123456789012345.6", 1234567890123456.0: "1.234567890123456e+15", 0.001: "0.001",
		0.0012: "0.0012", 12345678901234567.0: "1.2345678901234568e+16", 5e-324: "5.0e-324", 1.5e300: "1.5e+300",
	} {
		if got := RubyFloat(v); got != want {
			t.Errorf("RubyFloat(%v) = %s, want %s", v, got, want)
		}
	}
	if got := (Number{Value: 12345, Int: true}).String(); got != "12345" {
		t.Errorf("Integer = %s", got)
	}
}

func TestRecordJSON(t *testing.T) {
	r := Record{Date: "2026-09-28", Base: "EUR", Quote: "USD", Rate: Float(1.08), HasProviders: true,
		Providers: []Contribution{{Key: "A&B", Date: "2026-09-28", Rate: Number{Value: 6000, Int: true}, Excluded: true}}}
	data, err := r.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"date":"2026-09-28","base":"EUR","quote":"USD","rate":1.08,"providers":[{"key":"A&B","date":"2026-09-28","rate":6000,"excluded":true}]}`
	if string(data) != want {
		t.Fatalf("got  %s\nwant %s", data, want)
	}
	if !json.Valid(data) {
		t.Fatal("invalid JSON")
	}
	plain, _ := Record{Date: "d", Base: "b", Quote: "q", Rate: Float(1)}.MarshalJSON()
	if string(plain) != `{"date":"d","base":"b","quote":"q","rate":1}` {
		t.Fatalf("got %s", plain)
	}
}
