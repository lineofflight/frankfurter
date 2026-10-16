package rates

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// testdata/ruby_format.txt holds Ruby's format("%.<d>f", v) ("f<d>" lines) and
// format("%.12g", v) ("g12" lines) for random values, many of them decimal ties
// that the exact binary value rounds the other way.
func TestFormatRoundMatchesRuby(t *testing.T) {
	f, err := os.Open("testdata/ruby_format.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	n := 0
	for s.Scan() {
		fields := strings.Fields(s.Text())
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		want, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			t.Fatal(err)
		}
		var got float64
		if fields[0] == "g12" {
			got = formatRound(v, true, 12)
		} else {
			d, err := strconv.Atoi(fields[0][1:])
			if err != nil {
				t.Fatal(err)
			}
			got = formatRound(v, false, d)
		}
		if got != want {
			t.Errorf("%s %s: got %v, Ruby %v", fields[0], fields[1], got, want)
		}
		n++
	}
	if err := s.Err(); err != nil || n < 4000 {
		t.Fatalf("read %d samples: %v", n, err)
	}
}
