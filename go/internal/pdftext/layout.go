package pdftext

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// defaultFontSize is pdf-reader's PageLayout::DEFAULT_FONT_SIZE.
const defaultFontSize = 12

// Text lays the page's runs out on a character grid, as pdf-reader's
// PageLayout#to_s: rows are the mean font size tall and columns the median
// glyph width wide (plus 5%), each run is written at its grid position, and
// blank rows above and below the text are trimmed.
func (p Page) Text() string {
	if len(p.Runs) == 0 {
		return ""
	}

	meanFont := 0.0
	widths := make([]float64, len(p.Runs))
	xOffset, lowestY := math.Inf(1), math.Inf(1)
	for i, r := range p.Runs {
		meanFont += r.FontSize
		widths[i] = r.Width / float64(utf8.RuneCountInString(r.Text))
		xOffset = math.Min(xOffset, r.X)
		lowestY = math.Min(lowestY, r.Y)
	}
	meanFont /= float64(len(p.Runs))
	if meanFont == 0 {
		meanFont = defaultFontSize
	}
	slices.Sort(widths)
	medianWidth := widths[len(widths)/2]
	yOffset := 0.0
	if lowestY <= 0 {
		yOffset = lowestY
	}

	rowCount := int(math.Floor(p.MediaBox.Height() / meanFont))
	if rowCount <= 0 || medianWidth <= 0 {
		return ""
	}
	colCount := int(math.Floor(p.MediaBox.Width() / medianWidth * 1.05))
	rowHeight := p.MediaBox.Height() / float64(rowCount)
	colWidth := p.MediaBox.Width() / float64(colCount)

	rows := make([][]rune, rowCount)
	for i := range rows {
		rows[i] = []rune(strings.Repeat(" ", colCount))
	}
	for _, r := range p.Runs {
		x := int(math.Round((r.X - xOffset) / colWidth))
		y := rowCount - int(math.Round((r.Y-yOffset)/rowHeight))
		if y < 0 || y > rowCount || x < 0 || x > colCount {
			continue
		}
		row := y - 1
		if row < 0 {
			row = rowCount - 1 // Ruby's page[-1]: a run on the bottom edge lands on the last row
		}
		rows[row] = insert(rows[row], []rune(r.Text), x)
	}

	lines := make([]string, len(rows))
	first, last := -1, -1
	for i, row := range rows {
		lines[i] = strings.TrimRight(string(row), rubySpace)
		if strings.Trim(lines[i], rubySpace) != "" {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return ""
	}
	return strings.Join(lines[first:last+1], "\n")
}

// rubySpace is what Ruby's strip and rstrip remove.
const rubySpace = " \t\n\v\f\r\x00"

// insert overwrites row from index onwards with text, growing the row if text
// runs past its end, as Ruby's haystack[index, needle.length] = needle.
func insert(row, text []rune, index int) []rune {
	end := index + len(text)
	if end > len(row) {
		row = append(row, make([]rune, end-len(row))...)
	}
	copy(row[index:], text)
	return row
}
