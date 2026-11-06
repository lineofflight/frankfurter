package pdftext

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"

	"github.com/lineofflight/frankfurter/go/internal/vcrtest"
)

// recorded returns the response body of one cassette interaction.
func recorded(t *testing.T, name string, index int) []byte {
	t.Helper()
	c, err := cassette.Load(filepath.Join(vcrtest.CassetteDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(c.Interactions[index].Response.Body)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func findRun(runs []Run, text string) (Run, bool) {
	i := slices.IndexFunc(runs, func(r Run) bool { return r.Text == text })
	if i < 0 {
		return Run{}, false
	}
	return runs[i], true
}

func near(a, b, tolerance float64) bool { return math.Abs(a-b) <= tolerance }

// The values below are pdf-reader's for the same PDF.
func TestPagesMatchPDFReaderGeometry(t *testing.T) {
	pages, err := Pages(recorded(t, "jpc", 1)) // encrypted with an empty user password
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Fatalf("got %d pages, want 3", len(pages))
	}
	tests := []struct {
		text              string
		x, y, width, size float64
	}{
		{"USD", 366.96, 569.3604, 21.27888, 10.08},
		{"109.47", 432, 568.4003, 36.696, 12},
		{"KRW", 365.52, 536.2404, 23.51664, 10.08},
		{"9.38", 526.08, 535.2803, 23.352, 12},
	}
	for _, tt := range tests {
		r, ok := findRun(pages[0].Runs, tt.text)
		if !ok {
			t.Errorf("no run %q", tt.text)
			continue
		}
		if !near(r.X, tt.x, 0.01) || !near(r.Y, tt.y, 0.01) || !near(r.Width, tt.width, 0.2) || r.FontSize != tt.size {
			t.Errorf("%q = %+v, want x=%v y=%v width=%v size=%v", tt.text, r, tt.x, tt.y, tt.width, tt.size)
		}
	}
}

func TestTextMatchesPDFReaderLayout(t *testing.T) {
	text, err := Text(recorded(t, "bm", 2))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(text, "\n")
	for _, want := range []string{
		"                                        MERCADO CAMBIAL",
		"PAÍSES                  MOEDAS           COMPRA          VENDA       MÉDIA",
		"Estados Unidos(a)                        63,27           64,54        63,91",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("text lacks line %q", want)
		}
	}
}

// rotated.pdf is synthetic: four pages with /Rotate 0, 90, 180 and 270, each
// drawing the same notice through a matrix that undoes its rotation, so all
// four display it upright. The values below are pdf-reader's for the same file;
// the grid's y offset gives the 90 and 180 pages an extra blank line.
func TestPagesTurnRotatedPagesAsPDFReader(t *testing.T) {
	pages, err := Pages(fixture(t, "rotated.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 4 {
		t.Fatalf("got %d pages, want 4", len(pages))
	}
	tests := []struct {
		media Rect
		x, y  float64 // origin of "U.S. DOLLAR (USD)"
		gap   string  // between the EUR and GYD rows
	}{
		{Rect{0, 0, 612, 792}, 40, 580, "\n\n"},
		{Rect{0, -792, 612, 0}, 40, -212, "\n\n\n"},
		{Rect{-612, -792, 0, 0}, -572, -212, "\n\n\n"},
		{Rect{-612, 0, 0, 792}, -572, 580, "\n\n"},
	}
	for i, tt := range tests {
		p := pages[i]
		if p.MediaBox != tt.media {
			t.Errorf("page %d media box = %v, want %v", i+1, p.MediaBox, tt.media)
		}
		r, ok := findRun(p.Runs, "U.S. DOLLAR (USD)")
		if !ok {
			t.Errorf("page %d: no USD row label", i+1)
		} else if !near(r.X, tt.x, 0.01) || !near(r.Y, tt.y, 0.01) || !near(r.Width, 92.79, 0.2) || r.FontSize != 10 {
			t.Errorf("page %d: USD label = %+v, want x=%v y=%v width=92.79 size=10", i+1, r, tt.x, tt.y)
		}
		want := strings.Repeat(" ", 29) + "WISSELKOERSNOTERINGEN IN SRD\n\n" +
			strings.Repeat(" ", 31) + "0" + string(rune('3'+i)) + " JUNI 2013 EN TOT NADER ORDER\n\n\n\n\n\n" +
			"GELDSOORT" + strings.Repeat(" ", 50) + "AANKOOP*        VERKOOP*       AANKOOP*       VERKOOP*\n\n\n\n" +
			"U.S. DOLLAR (USD)" + strings.Repeat(" ", 44) + "3,250           3,350          3,250          3,350\n\n\n" +
			"EURO (EUR)" + strings.Repeat(" ", 51) + "4,228           4,358          4,215          4,368" + tt.gap +
			"GUYANA DOLLAR (PER 100 GYD)" + strings.Repeat(" ", 34) + "1,580           1,650          1,560          1,660"
		if got := p.Text(); got != want {
			t.Errorf("page %d text = %q, want %q", i+1, got, want)
		}
	}
}

// Page 105 of CBVS's 2013 yearly file is a scan turned by /Rotate 270, with no
// text on it. pdf-reader turns its media box and reads nothing.
func TestPagesReadRotatedScan(t *testing.T) {
	pages, err := Pages(fixture(t, "cbvs-2013-p105.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	p := pages[0]
	if want := (Rect{-612, 0, 0, 792}); p.MediaBox != want {
		t.Errorf("media box = %v, want %v", p.MediaBox, want)
	}
	if len(p.Runs) != 0 || p.Text() != "" {
		t.Errorf("runs = %+v, text = %q, want none", p.Runs, p.Text())
	}
}

func TestRectRotatedAsPDFReader(t *testing.T) {
	box := Rect{10, 20, 110, 220}
	tests := map[int]Rect{
		0:   box,
		90:  {10, -80, 210, 20},
		180: {-90, -180, 10, 20},
		270: {-190, 20, 10, 120},
	}
	for degrees, want := range tests {
		if got := box.rotated(degrees); got != want {
			t.Errorf("rotated(%d) = %v, want %v", degrees, got, want)
		}
	}
}

func glyph(x, y float64, text string) Run {
	return Run{X: x, Y: y, Width: 6, FontSize: 10, Text: text}
}

func TestRunsMergeNeighbours(t *testing.T) {
	got := runs([]Run{
		glyph(10, 100.4, "U"), glyph(16, 100.2, "S"), glyph(22, 100, "D"), // adjacent: one word
		glyph(35, 100, "1"),  // a gap wider than a fifth of the font size: joined with a space
		glyph(100, 100, "9"), // too far: a new run
		glyph(10, 80, "E"),   // next line
	}, Rect{0, 0, 600, 800})
	var texts []string
	for _, r := range got {
		texts = append(texts, r.Text)
	}
	if want := []string{"USD 1", "9", "E"}; !slices.Equal(texts, want) {
		t.Errorf("runs = %q, want %q", texts, want)
	}
	if got[0].Width != 31 {
		t.Errorf("merged width = %v, want 31", got[0].Width)
	}
}

func TestRunsDropDuplicatesAndOutsiders(t *testing.T) {
	got := runs([]Run{
		glyph(10, 100, "A"),
		glyph(10.5, 100, "A"), // painted again to fake bold
		{X: 30, Y: 100, Width: 0, FontSize: 10, Text: "B"}, // zero width
		glyph(700, 100, "C"), // outside the crop box
	}, Rect{0, 0, 600, 800})
	if len(got) != 1 || got[0].Text != "A" {
		t.Errorf("runs = %+v, want the single A", got)
	}
}

func TestTextLaysRunsOnAGrid(t *testing.T) {
	p := Page{MediaBox: Rect{0, 0, 120, 60}, Runs: []Run{
		{X: 0, Y: 40, Width: 18, FontSize: 10, Text: "USD"},
		{X: 60, Y: 40, Width: 30, FontSize: 10, Text: "155.39"},
		{X: 0, Y: 20, Width: 18, FontSize: 10, Text: "EUR"},
	}}
	want := "USD        155.39\n\nEUR"
	if got := p.Text(); got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}
