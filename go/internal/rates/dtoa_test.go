package rates

import "testing"

// Expected values come from Ruby: Float(format("%.<n>f" or "%.<n>g", v)). The
// first four are near ties the old shortest-decimal rule got wrong; an
// 800,000-value sweep of API-style products, noisy ties and random doubles
// matched Ruby exactly.
func TestFormatRoundNearTies(t *testing.T) {
	cases := []struct {
		v    float64
		sig  bool
		n    int
		want float64
	}{
		{4.1409 * 5, false, 3, 20.704},
		{0.24500000000000002, false, 2, 0.24},
		{10.095374999999999, false, 5, 10.09538},
		{36639718.68749999, false, 3, 36639718.688},
		{214.415, false, 2, 214.42},
		{643.965, false, 2, 643.96},
		{1.005, false, 2, 1.0},
		{0.125, false, 2, 0.12},
		{5.0e-07, false, 6, 0.0},
		{0.005, false, 2, 0.01},
		{9.9999999, false, 4, 10.0},
		{999.999, false, 2, 1000.0},
		{181.64319999999998, true, 12, 181.6432},
		{1.0000000000005, true, 12, 1.0},
		{123456789012.5, true, 12, 123456789012.0},
		{0.30000000000000004, true, 12, 0.3},
		{1234.5, true, 4, 1234.0},
		{1.0e-310, false, 6, 0.0},
		{0.00015, false, 5, 0.00015},
		{-20.704500000000003, false, 3, -20.704},
	}
	for _, c := range cases {
		if got := formatRound(c.v, c.sig, c.n); got != c.want {
			t.Errorf("formatRound(%v, %v, %d) = %v, want %v", c.v, c.sig, c.n, got, c.want)
		}
	}
}
