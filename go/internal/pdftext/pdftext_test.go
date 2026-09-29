package pdftext

import (
	"math"
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
