package rates

import (
	"math"
	"strconv"
	"strings"
)

// formatRound is Float(format("%.<n>g" or "%.<n>f", v)) as Ruby computes it: to n significant digits when sig is
// set, else to n decimals.
//
// Ruby's format rounds through its copy of David Gay's dtoa (missing/dtoa.c, mode 2 for %g, mode 3 for %f), and Ruby
// patched its floating-point fast path: when the discarded tail lies within the fast path's error bound of one half,
// it rounds half to even instead of deferring to exact arithmetic. So format("%.3f", 4.1409 * 5) is 20.704 although
// the double is 20.704500000000003, and format("%.2f", 214.415) is 214.42 although the double lies just below
// 214.415. Everything else is the exact binary value correctly rounded, as strconv does it.
func formatRound(v float64, sig bool, n int) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	a := math.Abs(v)
	var out float64
	if digits, point, ok := dtoaQuick(a, sig, n); ok {
		out = parseDigits(digits, point)
	} else if sig {
		out, _ = strconv.ParseFloat(strconv.FormatFloat(a, 'e', n-1, 64), 64)
	} else {
		out, _ = strconv.ParseFloat(strconv.FormatFloat(a, 'f', n, 64), 64)
	}
	return math.Copysign(out, v)
}

// parseDigits reads 0.digits × 10^point; no digits is zero.
func parseDigits(digits []byte, point int) float64 {
	if len(digits) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(string(digits)+"e"+strconv.Itoa(point-len(digits)), 64)
	return f
}

var (
	dtoaTens    = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}
	dtoaBigTens = [...]float64{1e16, 1e32, 1e64, 1e128, 1e256}
)

const (
	dtoaQuickMax = 14
	dtoaBletch   = 0x10
)

// dtoaQuick is dtoa's floating-point fast path for a positive, finite a, with Ruby's half-to-even patch. It returns
// the rounded digits and decimal point (a ≈ 0.digits × 10^point), or false where dtoa falls back to exact arithmetic.
// Explicit float64 conversions keep Go from fusing multiply-adds that the C code evaluates as separate operations.
func dtoaQuick(a float64, sig bool, n int) ([]byte, int, bool) {
	bits := math.Float64bits(a)
	biased := int(bits >> 52 & 0x7ff)
	if biased == 0 {
		return nil, 0, false // subnormal
	}
	// dtoa's estimate of floor(log10(a)), exact for 0 <= k <= 22 and possibly one too high elsewhere (k_check).
	d2 := math.Float64frombits(bits&(1<<52-1) | 0x3ff<<52)
	i := biased - 1023
	ds := float64(float64((d2-1.5)*0.289529654602168)+0.1760912590558) + float64(float64(i)*0.301029995663981)
	k := int(ds)
	if ds < 0 && ds != float64(k) {
		k--
	}
	kCheck := true
	if k >= 0 && k < len(dtoaTens) {
		if a < dtoaTens[k] {
			k--
		}
		kCheck = false
	}

	var ilim, ilim1 int
	if sig {
		if n <= 0 {
			n = 1
		}
		ilim, ilim1 = n, n
	} else {
		ilim = n + k + 1
		ilim1 = ilim - 1
	}
	if ilim < 0 || ilim > dtoaQuickMax {
		return nil, 0, false
	}

	d := a
	ieps := 2
	if k > 0 {
		ds := dtoaTens[k&0xf]
		j := k >> 4
		if j&dtoaBletch != 0 {
			j &= dtoaBletch - 1
			d /= dtoaBigTens[len(dtoaBigTens)-1]
			ieps++
		}
		for idx := 0; j != 0; j, idx = j>>1, idx+1 {
			if j&1 != 0 {
				ieps++
				ds *= dtoaBigTens[idx]
			}
		}
		d /= ds
	} else if j1 := -k; j1 != 0 {
		d *= dtoaTens[j1&0xf]
		for j, idx := j1>>4, 0; j != 0; j, idx = j>>1, idx+1 {
			if j&1 != 0 {
				ieps++
				d *= dtoaBigTens[idx]
			}
		}
	}
	if kCheck && d < 1 && ilim > 0 {
		if ilim1 <= 0 {
			return nil, 0, false
		}
		ilim = ilim1
		k--
		d *= 10
		ieps++
	}
	eps := math.Ldexp(float64(float64(ieps)*d)+7, -52)

	if ilim == 0 {
		d -= 5
		switch {
		case d > eps:
			return []byte{'1'}, k + 2, true // one unit of the last place
		case d < -eps:
			return nil, 0, true
		}
		return nil, 0, false
	}

	eps *= dtoaTens[ilim-1]
	var digits []byte
	for i := 1; ; i++ {
		l := int64(d)
		d -= float64(l)
		if d == 0 {
			ilim = i
		}
		digits = append(digits, byte('0'+l))
		if i == ilim {
			switch {
			case d > 0.5+eps:
				return bumpUp(digits, k+1)
			case d < 0.5-eps:
				return digits, k + 1, true
			case l&1 != 0:
				return bumpUp(digits, k+1) // Ruby's patch: a near tie rounds to even
			}
			// An even near tie: Ruby takes the exact path but never rounds the even digit up, which leaves these
			// digits (the tail is too close to one half for the digits themselves to be in doubt).
			return digits, k + 1, true
		}
		d *= 10
	}
}

// bumpUp adds one to the last digit, carrying; all nines become 1 with the point moved up.
func bumpUp(digits []byte, point int) ([]byte, int, bool) {
	s := strings.TrimRight(string(digits), "9")
	if s == "" {
		return []byte{'1'}, point + 1, true
	}
	out := []byte(s)
	out[len(out)-1]++
	return out, point, true
}
