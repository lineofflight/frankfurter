// Package xls reads legacy Excel (BIFF8 .xls) workbooks with the cell typing of Ruby's spreadsheet gem, which the
// Ruby adapters were written against: numbers come back as numbers, text as text, and a number whose format looks like
// a date as a date.
//
// Formula cells read as empty. The gem returns them as Spreadsheet::Formula, which no adapter treats as a number.
//
// BIFF8 is frozen, so this is a small reader rather than a library: github.com/richardlehane/mscfb opens the Compound
// File container, and biff.go reads only the records the adapters need from its Workbook stream. [MS-XLS] documents
// the record layouts.
package xls

import (
	"fmt"
	"math"
	"regexp"
	"time"
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
	stream, err := workbookStream(data)
	if err != nil {
		return nil, fmt.Errorf("xls: %w", err)
	}
	globals, err := substream(stream, 0)
	if err != nil {
		return nil, fmt.Errorf("xls: %w", err)
	}
	wb, err := readWorkbook(globals)
	if err != nil {
		return nil, fmt.Errorf("xls: %w", err)
	}
	sheets := make([]Sheet, len(wb.sheets))
	for i, bs := range wb.sheets {
		recs, err := substream(stream, bs.pos)
		if err != nil {
			return nil, fmt.Errorf("xls: sheet %d: %w", i, err)
		}
		rows, err := wb.readRows(recs)
		if err != nil {
			return nil, fmt.Errorf("xls: sheet %d: %w", i, err)
		}
		for j := range rows {
			rows[j] = trimBlank(rows[j])
		}
		sheets[i] = Sheet{Name: bs.name, Rows: rows}
	}
	return sheets, nil
}

// number types a NUMBER or RK value by its cell's number format, as the gem does.
func (wb *workbook) number(xf uint16, v float64) Cell {
	if int(xf) < len(wb.xfs) && isDate(wb.numberFormat(wb.xfs[xf])) {
		return Cell{Kind: Date, Date: serialDate(v)}
	}
	return Cell{Kind: Number, Number: v}
}

func trimBlank(cells Row) Row {
	n := len(cells)
	for n > 0 && cells[n-1].Kind == Empty {
		n--
	}
	return cells[:n]
}

// builtinFormats are Excel's implicit number formats, as the gem names them.
var builtinFormats = map[uint16]string{
	0: "GENERAL", 1: "0", 2: "0.00", 3: "#,##0", 4: "#,##0.00", 9: "0%", 10: "0.00%", 11: "0.00E+00",
	12: "# ?/?", 13: "# ??/??", 14: "M/D/YY", 15: "D-MMM-YY", 16: "D-MMM", 17: "MMM-YY", 18: "h:mm AM/PM",
	19: "h:mm:ss AM/PM", 20: "h:mm", 21: "h:mm:ss", 22: "M/D/YY h:mm", 45: "mm:ss", 46: "[h]:mm:ss", 47: "mm:ss.0",
	48: "##0.0E+0", 49: "@",
}

func (wb *workbook) numberFormat(index uint16) string {
	if f, ok := builtinFormats[index]; ok {
		return f
	}
	return wb.formats[index]
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
