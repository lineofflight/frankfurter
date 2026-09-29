package xls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"
)

// The BIFF8 records the reader uses. Everything else, formulas included, is skipped.
const (
	recEOF        = 0x000A
	recContinue   = 0x003C
	recBoundSheet = 0x0085
	recMulRK      = 0x00BD
	recMulBlank   = 0x00BE
	recXF         = 0x00E0
	recSST        = 0x00FC
	recLabelSST   = 0x00FD
	recBlank      = 0x0201
	recNumber     = 0x0203
	recLabel      = 0x0204
	recBoolErr    = 0x0205
	recRK         = 0x027E
	recFormat     = 0x041E
	recBOF        = 0x0809
)

const biff8 = 0x0600

var (
	le           = binary.LittleEndian
	errTruncated = errors.New("truncated record")
)

type record struct {
	typ  uint16
	data []byte
}

// workbook is what the globals substream says about the sheets' cells.
type workbook struct {
	sheets  []boundSheet
	sst     []string
	xfs     []uint16          // each XF's number format index
	formats map[uint16]string // custom number formats by index
}

type boundSheet struct {
	name string
	pos  int // offset of the sheet's BOF in the Workbook stream
}

// workbookStream pulls the Workbook stream, which holds the BIFF records, out of the Compound File container.
func workbookStream(data []byte) ([]byte, error) {
	doc, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	for _, f := range doc.File {
		if f.Name == "Workbook" && len(f.Path) == 0 {
			stream := make([]byte, f.Size)
			if _, err := io.ReadFull(f, stream); err != nil {
				return nil, err
			}
			return stream, nil
		}
	}
	return nil, errors.New("no Workbook stream (not a BIFF8 workbook)")
}

// substream splits the records from the BOF at pos to its matching EOF, stepping over any nested substream such as an
// embedded chart.
func substream(stream []byte, pos int) ([]record, error) {
	var recs []record
	depth := 0
	for {
		if pos < 0 || pos+4 > len(stream) {
			return nil, errTruncated
		}
		typ, n := le.Uint16(stream[pos:]), int(le.Uint16(stream[pos+2:]))
		if pos+4+n > len(stream) {
			return nil, errTruncated
		}
		recs = append(recs, record{typ, stream[pos+4 : pos+4+n]})
		pos += 4 + n
		switch typ {
		case recBOF:
			depth++
		case recEOF:
			if depth--; depth <= 0 {
				return recs, nil
			}
		}
	}
}

func readWorkbook(recs []record) (*workbook, error) {
	wb := &workbook{formats: map[uint16]string{}}
	for i := 0; i < len(recs); i++ {
		d := recs[i].data
		switch recs[i].typ {
		case recBOF:
			if len(d) < 2 || le.Uint16(d) != biff8 {
				return nil, errors.New("not a BIFF8 workbook")
			}
		case recBoundSheet:
			if len(d) < 6 {
				return nil, errTruncated
			}
			name, err := unicodeString(d[6:], 1)
			if err != nil {
				return nil, fmt.Errorf("sheet name: %w", err)
			}
			wb.sheets = append(wb.sheets, boundSheet{name, int(le.Uint32(d))})
		case recFormat:
			if len(d) < 2 {
				return nil, errTruncated
			}
			format, err := unicodeString(d[2:], 2)
			if err != nil {
				return nil, fmt.Errorf("number format: %w", err)
			}
			wb.formats[le.Uint16(d)] = format
		case recXF:
			if len(d) < 4 {
				return nil, errTruncated
			}
			wb.xfs = append(wb.xfs, le.Uint16(d[2:]))
		case recSST:
			segs := [][]byte{d}
			for i+1 < len(recs) && recs[i+1].typ == recContinue {
				i++
				segs = append(segs, recs[i].data)
			}
			sst, err := readSST(segs)
			if err != nil {
				return nil, fmt.Errorf("shared strings: %w", err)
			}
			wb.sst = sst
		}
	}
	return wb, nil
}

// readRows collects a worksheet's cells into rows. Blank cells count toward the row count but read as empty.
func (wb *workbook) readRows(recs []record) ([]Row, error) {
	var rows []Row
	set := func(r, c int, cell Cell) {
		for len(rows) <= r {
			rows = append(rows, Row{})
		}
		for len(rows[r]) <= c {
			rows[r] = append(rows[r], Cell{})
		}
		rows[r][c] = cell
	}
	for _, rec := range recs {
		d := rec.data
		switch rec.typ {
		case recNumber, recRK, recMulRK, recLabelSST, recLabel, recBoolErr, recBlank, recMulBlank:
			if len(d) < 6 {
				return nil, errTruncated
			}
		default:
			continue
		}
		r, c, xf := int(le.Uint16(d)), int(le.Uint16(d[2:])), le.Uint16(d[4:])
		switch rec.typ {
		case recNumber:
			if len(d) < 14 {
				return nil, errTruncated
			}
			set(r, c, wb.number(xf, math.Float64frombits(le.Uint64(d[6:]))))
		case recRK:
			if len(d) < 10 {
				return nil, errTruncated
			}
			set(r, c, wb.number(xf, rk(le.Uint32(d[6:]))))
		case recMulRK: // row, first column, then an XF index and RK number per column, then the last column
			for i := range (len(d) - 6) / 6 {
				v := d[4+6*i:]
				set(r, c+i, wb.number(le.Uint16(v), rk(le.Uint32(v[2:]))))
			}
		case recLabelSST:
			if len(d) < 10 {
				return nil, errTruncated
			}
			i := int(le.Uint32(d[6:]))
			if i >= len(wb.sst) {
				return nil, fmt.Errorf("shared string %d of %d", i, len(wb.sst))
			}
			set(r, c, Cell{Kind: String, String: wb.sst[i]})
		case recLabel:
			s, err := unicodeString(d[6:], 2)
			if err != nil {
				return nil, err
			}
			set(r, c, Cell{Kind: String, String: s})
		case recBoolErr:
			if len(d) < 8 {
				return nil, errTruncated
			}
			set(r, c, boolErr(d[6], d[7]))
		case recBlank:
			set(r, c, Cell{})
		case recMulBlank: // row, first column, then an XF index per column, then the last column
			for i := range (len(d) - 6) / 2 {
				set(r, c+i, Cell{})
			}
		}
	}
	return rows, nil
}

// rk decodes an RK number: a 30-bit signed integer or the top 30 bits of a float64, either one optionally divided by 100.
func rk(v uint32) float64 {
	var n float64
	if v&2 != 0 {
		n = float64(int32(v) >> 2)
	} else {
		n = math.Float64frombits(uint64(v&^3) << 32)
	}
	if v&1 != 0 {
		n /= 100
	}
	return n
}

var errorCodes = map[byte]string{
	0x00: "#NULL!", 0x07: "#DIV/0!", 0x0F: "#VALUE!", 0x17: "#REF!", 0x1D: "#NAME?", 0x24: "#NUM!", 0x2A: "#N/A",
}

func boolErr(value, isError byte) Cell {
	if isError == 0 {
		return Cell{Kind: Bool, Number: float64(value)}
	}
	code, ok := errorCodes[value]
	if !ok {
		code = fmt.Sprint(value)
	}
	return Cell{Kind: Error, String: code}
}

// unicodeString reads a string that sits whole in one record: a character count of lenSize bytes, a flags byte whose
// low bit marks UTF-16 over compressed Latin-1, then the characters.
func unicodeString(b []byte, lenSize int) (string, error) {
	if len(b) < lenSize+1 {
		return "", errTruncated
	}
	n := int(b[0])
	if lenSize == 2 {
		n = int(le.Uint16(b))
	}
	c := &cursor{segs: [][]byte{b[lenSize+1:]}}
	s := c.chars(n, b[lenSize]&1 != 0)
	return s, c.err
}

// readSST reads the shared string table, which runs on across CONTINUE records. See [MS-XLS] 2.4.265 and 2.5.293.
func readSST(segs [][]byte) ([]string, error) {
	c := &cursor{segs: segs}
	c.bytes(4) // total string count, including repeats
	b := c.bytes(4)
	if c.err != nil {
		return nil, c.err
	}
	var sst []string
	for range le.Uint32(b) {
		b := c.bytes(3)
		if c.err != nil {
			return nil, c.err
		}
		n, flags := int(le.Uint16(b)), b[2]
		var skip int
		if flags&0x08 != 0 { // rich text: 4 bytes per formatting run, after the characters
			if b := c.bytes(2); c.err == nil {
				skip += 4 * int(le.Uint16(b))
			}
		}
		if flags&0x04 != 0 { // phonetic data, after the runs
			if b := c.bytes(4); c.err == nil {
				skip += int(le.Uint32(b))
			}
		}
		s := c.chars(n, flags&1 != 0)
		c.bytes(skip)
		if c.err != nil {
			return nil, c.err
		}
		sst = append(sst, s)
	}
	return sst, nil
}

// cursor reads a record's data and its CONTINUE records' as one run of bytes.
type cursor struct {
	segs [][]byte // data of the record and each CONTINUE, the current one first
	pos  int      // offset in segs[0]
	err  error
}

// bytes reads n bytes, crossing into the next CONTINUE as needed.
func (c *cursor) bytes(n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n && c.err == nil {
		if c.pos == len(c.segs[0]) {
			c.advance()
			continue
		}
		k := min(n-len(out), len(c.segs[0])-c.pos)
		out = append(out, c.segs[0][c.pos:c.pos+k]...)
		c.pos += k
	}
	return out
}

func (c *cursor) advance() {
	if len(c.segs) == 1 {
		c.err = errTruncated
		return
	}
	c.segs, c.pos = c.segs[1:], 0
}

// chars reads n characters, compressed (one Latin-1 byte each) or UTF-16. When the characters are split across a
// CONTINUE, it begins with a fresh flags byte, since the rest may be stored the other way.
func (c *cursor) chars(n int, wide bool) string {
	units := make([]uint16, 0, n)
	for len(units) < n && c.err == nil {
		if c.pos == len(c.segs[0]) {
			if flags := c.bytes(1); c.err == nil {
				wide = flags[0]&1 != 0
			}
			continue
		}
		b := c.segs[0][c.pos:]
		if !wide {
			k := min(n-len(units), len(b))
			for _, ch := range b[:k] {
				units = append(units, uint16(ch))
			}
			c.pos += k
			continue
		}
		k := min(n-len(units), len(b)/2)
		if k == 0 {
			c.err = errTruncated // half a UTF-16 unit
			break
		}
		for i := range k {
			units = append(units, le.Uint16(b[2*i:]))
		}
		c.pos += 2 * k
	}
	return string(utf16.Decode(units))
}
