// Package xls reads legacy Excel (BIFF8 .xls) workbooks with the cell typing of Ruby's spreadsheet gem, which the
// Ruby adapters were written against: numbers come back as numbers, text as text, and a number whose format looks like
// a date as a date.
//
// Formula cells read as empty. The gem returns them as Spreadsheet::Formula, which no adapter treats as a number.
package xls

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/shakinm/xlsReader/xls"
)

// Kind is a cell's type.
type Kind int

// Cell kinds.
const (
	Empty Kind = iota
	String
	Number
	Date
	Bool
	Error
)

// Cell is one worksheet cell.
type Cell struct {
	Kind   Kind
	String string    // String; Error's code such as "#N/A"
	Number float64   // Number; Bool as 0 or 1
	Date   time.Time // Date, UTC, with any time of day the format shows
}

// Text renders the cell as Ruby's cell.to_s would, for header and label matching.
func (c Cell) Text() string {
	switch c.Kind {
	case String, Error:
		return c.String
	case Number:
		return fmt.Sprint(c.Number)
	case Date:
		return c.Date.Format(time.DateOnly)
	case Bool:
		return fmt.Sprint(c.Number == 1)
	}
	return ""
}

// Row is a worksheet row, running to its last non-blank cell.
type Row []Cell

// At returns the cell in column i (zero-based), or an empty cell past the end of the row, as Ruby's row[i] gives nil.
func (r Row) At(i int) Cell {
	if i < 0 || i >= len(r) {
		return Cell{}
	}
	return r[i]
}

// Sheet is a worksheet's rows, indexed from the first row as the gem's sheet.each yields them.
type Sheet struct {
	Name string
	Rows []Row
}

// Open parses a workbook.
func Open(data []byte) ([]Sheet, error) {
	wb, err := xls.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("xls: %w", err)
	}
	sheets := make([]Sheet, wb.GetNumberSheets())
	for i := range sheets {
		ws, err := wb.GetSheet(i)
		if err != nil {
			return nil, fmt.Errorf("xls: sheet %d: %w", i, err)
		}
		sheets[i].Name = ws.GetName()
		for _, row := range ws.GetRows() {
			cols := row.GetCols()
			cells := make(Row, len(cols))
			for j, c := range cols {
				cells[j] = cell(&wb, c)
			}
			sheets[i].Rows = append(sheets[i].Rows, trimBlank(cells))
		}
	}
	return sheets, nil
}

type cellData interface {
	GetString() string
	GetFloat64() float64
	GetInt64() int64
	GetXFIndex() int
	GetType() string
}

func cell(wb *xls.Workbook, c cellData) Cell {
	switch c.GetType() {
	case "*record.LabelSSt", "*record.LabelBIFF8", "*record.LabelBIFF5", "*record.Label", "*record.Rstring":
		return Cell{Kind: String, String: latin1IfInvalid(c.GetString())}
	case "*record.Number", "*record.Rk":
		v := c.GetFloat64()
		xf := wb.GetXFbyIndex(c.GetXFIndex())
		format := numberFormat(wb, xf.GetFormatIndex())
		if isDate(format) {
			return Cell{Kind: Date, Date: serialDate(v)}
		}
		return Cell{Kind: Number, Number: v}
	case "*record.BoolErr":
		s := c.GetString()
		if s == "TRUE" || s == "FALSE" {
			return Cell{Kind: Bool, Number: float64(c.GetInt64())}
		}
		return Cell{Kind: Error, String: s}
	}
	return Cell{}
}

func trimBlank(cells Row) Row {
	n := len(cells)
	for n > 0 && cells[n-1].Kind == Empty {
		n--
	}
	return cells[:n]
}

// latin1IfInvalid decodes BIFF8's compressed strings, which the reader hands over as raw Latin-1 bytes.
func latin1IfInvalid(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	runes := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		runes[i] = rune(s[i])
	}
	return string(runes)
}

// builtinFormats are Excel's implicit number formats, as the gem names them.
var builtinFormats = map[int]string{
	0: "GENERAL", 1: "0", 2: "0.00", 3: "#,##0", 4: "#,##0.00", 9: "0%", 10: "0.00%", 11: "0.00E+00",
	12: "# ?/?", 13: "# ??/??", 14: "M/D/YY", 15: "D-MMM-YY", 16: "D-MMM", 17: "MMM-YY", 18: "h:mm AM/PM",
	19: "h:mm:ss AM/PM", 20: "h:mm", 21: "h:mm:ss", 22: "M/D/YY h:mm", 45: "mm:ss", 46: "[h]:mm:ss", 47: "mm:ss.0",
	48: "##0.0E+0", 49: "@",
}

func numberFormat(wb *xls.Workbook, index int) string {
	if f, ok := builtinFormats[index]; ok {
		return f
	}
	format := wb.GetFormatByIndex(index)
	return format.String()
}

// The gem's Spreadsheet::Format patterns: a format is a date or time when it has date or time tokens and no digit
// placeholders.
var (
	localePrefix  = regexp.MustCompile(`\A\[\$-\S+\]`)
	numberPattern = regexp.MustCompile(`[#]|0+`)
	datePattern   = regexp.MustCompile(`[YMD]|d{2}|m{3}|y{2}`)
	timePattern   = regexp.MustCompile(`[hms]`)
)

func isDate(format string) bool {
	format = localePrefix.ReplaceAllString(format, "")
	if numberPattern.MatchString(format) {
		return false
	}
	return datePattern.MatchString(format) || timePattern.MatchString(format)
}

// serialDate converts an Excel serial in the 1900 date system, rounding the time of day to the second as the gem does.
func serialDate(serial float64) time.Time {
	days := math.Floor(serial)
	secs := math.Round((serial - days) * 86400)
	return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days)).Add(time.Duration(secs) * time.Second)
}
