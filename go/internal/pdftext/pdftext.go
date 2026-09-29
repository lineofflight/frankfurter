// Package pdftext extracts positioned text from PDFs the way the Ruby adapters
// see it through pdf-reader.
//
// Glyphs come from PDFium (compiled to WebAssembly, so no cgo), which copes
// with the encrypted and legacy-encoded PDFs central banks publish. On top of
// them this package reproduces pdf-reader's post-processing, so a Go adapter
// can port Ruby logic that leans on it unchanged:
//
//   - Page.Runs is pdf-reader's Page#runs: glyphs inside the crop box, zero-width and overlapping duplicates
//     dropped, merged left to right into runs on the same line.
//   - Page.Text is pdf-reader's Page#text: those runs laid out on a character grid.
//
// Coordinates are PDF user space: points, origin at the bottom left, y growing
// upwards. Rotated pages are not supported.
//
// Where pdf-reader could not decode a glyph (old Japanese fonts without Unicode
// maps), PDFium usually can, so Go may read real text where Ruby read garbage.
// ISO codes and numbers come out the same.
package pdftext

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	pdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// Run is a stretch of text on one line, as pdf-reader's PDF::Reader::TextRun.
type Run struct {
	X, Y     float64 // origin of the first glyph (baseline, left edge)
	Width    float64
	FontSize float64
	Text     string
}

// EndX is the right edge of the run.
func (r Run) EndX() float64 { return r.X + r.Width }

// EndY is the top of the run's em box.
func (r Run) EndY() float64 { return r.Y + r.FontSize }

// Rect is a page box.
type Rect struct {
	Left, Bottom, Right, Top float64
}

// Width is the box's width.
func (r Rect) Width() float64 { return r.Right - r.Left }

// Height is the box's height.
func (r Rect) Height() float64 { return r.Top - r.Bottom }

func (r Rect) contains(x, y float64) bool {
	return x >= r.Left && x <= r.Right && y >= r.Bottom && y <= r.Top
}

// Page is one page's text.
type Page struct {
	MediaBox Rect
	Runs     []Run
}

// unknownGlyph is what pdf-reader shows for a glyph it cannot map to Unicode.
const unknownGlyph = "▯"

var (
	poolOnce sync.Once
	pool     pdfium.Pool
	poolErr  error
)

func instance() (pdfium.Pdfium, error) {
	poolOnce.Do(func() {
		pool, poolErr = webassembly.Init(webassembly.Config{MinIdle: 0, MaxIdle: 1, MaxTotal: 2})
	})
	if poolErr != nil {
		return nil, fmt.Errorf("pdftext: start PDFium: %w", poolErr)
	}
	return pool.GetInstance(5 * time.Minute)
}

// Pages reads every page of a PDF. Encrypted PDFs open with the empty user
// password.
func Pages(data []byte) ([]Page, error) {
	pdf, err := instance()
	if err != nil {
		return nil, err
	}
	defer pdf.Close()

	password := ""
	doc, err := pdf.OpenDocument(&requests.OpenDocument{File: &data, Password: &password})
	if err != nil {
		return nil, fmt.Errorf("pdftext: open: %w", err)
	}
	defer pdf.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})

	count, err := pdf.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return nil, fmt.Errorf("pdftext: %w", err)
	}
	pages := make([]Page, count.PageCount)
	for i := range pages {
		if pages[i], err = readPage(pdf, doc.Document, i); err != nil {
			return nil, fmt.Errorf("pdftext: page %d: %w", i+1, err)
		}
	}
	return pages, nil
}

// Text is every page's Text joined with newlines, as
// reader.pages.map(&:text).join("\n").
func Text(data []byte) (string, error) {
	pages, err := Pages(data)
	if err != nil {
		return "", err
	}
	texts := make([]string, len(pages))
	for i, p := range pages {
		texts[i] = p.Text()
	}
	return strings.Join(texts, "\n"), nil
}

func readPage(pdf pdfium.Pdfium, doc references.FPDF_DOCUMENT, index int) (Page, error) {
	loaded, err := pdf.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: doc, Index: index})
	if err != nil {
		return Page{}, err
	}
	defer pdf.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: loaded.Page})
	page := requests.Page{ByReference: &loaded.Page}

	rotation, err := pdf.FPDFPage_GetRotation(&requests.FPDFPage_GetRotation{Page: page})
	if err != nil {
		return Page{}, err
	}
	if rotation.PageRotation != 0 {
		return Page{}, errors.New("rotated pages are not supported")
	}

	media, err := mediaBox(pdf, page)
	if err != nil {
		return Page{}, err
	}
	crop := media
	if box, err := pdf.FPDFPage_GetCropBox(&requests.FPDFPage_GetCropBox{Page: page}); err == nil {
		crop = Rect{float64(box.Left), float64(box.Bottom), float64(box.Right), float64(box.Top)}
	}

	glyphs, err := readGlyphs(pdf, page)
	if err != nil {
		return Page{}, err
	}
	return Page{MediaBox: media, Runs: runs(glyphs, crop)}, nil
}

func mediaBox(pdf pdfium.Pdfium, page requests.Page) (Rect, error) {
	if box, err := pdf.FPDFPage_GetMediaBox(&requests.FPDFPage_GetMediaBox{Page: page}); err == nil {
		return Rect{float64(box.Left), float64(box.Bottom), float64(box.Right), float64(box.Top)}, nil
	}
	// No MediaBox on the page itself (it is inherited): fall back to the page
	// size PDFium resolved.
	w, err := pdf.FPDF_GetPageWidthF(&requests.FPDF_GetPageWidthF{Page: page})
	if err != nil {
		return Rect{}, err
	}
	h, err := pdf.FPDF_GetPageHeightF(&requests.FPDF_GetPageHeightF{Page: page})
	if err != nil {
		return Rect{}, err
	}
	return Rect{0, 0, float64(w.PageWidth), float64(h.PageHeight)}, nil
}

// readGlyphs returns one run per painted glyph, as pdf-reader's
// PageTextReceiver collects them.
func readGlyphs(pdf pdfium.Pdfium, page requests.Page) ([]Run, error) {
	text, err := pdf.FPDFText_LoadPage(&requests.FPDFText_LoadPage{Page: page})
	if err != nil {
		return nil, err
	}
	defer pdf.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: text.TextPage})
	tp := text.TextPage

	count, err := pdf.FPDFText_CountChars(&requests.FPDFText_CountChars{TextPage: tp})
	if err != nil {
		return nil, err
	}
	var glyphs []Run
	for i := 0; i < count.Count; i++ {
		generated, err := pdf.FPDFText_IsGenerated(&requests.FPDFText_IsGenerated{TextPage: tp, Index: i})
		if err != nil {
			return nil, err
		}
		if generated.IsGenerated {
			continue // PDFium's own spaces and line breaks
		}
		unicode, err := pdf.FPDFText_GetUnicode(&requests.FPDFText_GetUnicode{TextPage: tp, Index: i})
		if err != nil {
			return nil, err
		}
		char := unknownGlyph
		if unicode.Unicode != 0 {
			char = string(rune(unicode.Unicode))
		}
		if char == " " {
			continue // pdf-reader advances past spaces without recording them
		}
		origin, err := pdf.FPDFText_GetCharOrigin(&requests.FPDFText_GetCharOrigin{TextPage: tp, Index: i})
		if err != nil {
			return nil, err
		}
		box, err := pdf.FPDFText_GetLooseCharBox(&requests.FPDFText_GetLooseCharBox{TextPage: tp, Index: i})
		if err != nil {
			return nil, err
		}
		size, err := pdf.FPDFText_GetFontSize(&requests.FPDFText_GetFontSize{TextPage: tp, Index: i})
		if err != nil {
			return nil, err
		}
		matrix, err := pdf.FPDFText_GetMatrix(&requests.FPDFText_GetMatrix{TextPage: tp, Index: i})
		if err != nil {
			return nil, err
		}
		// pdf-reader's font size is the em height in device space: the nominal
		// size scaled by the text and graphics matrices. PDFium hands the
		// matrix over in float32, so round its noise away.
		effective := math.Abs(size.FontSize * float64(matrix.Matrix.B+matrix.Matrix.D))
		glyphs = append(glyphs, Run{
			X:        origin.X,
			Y:        origin.Y,
			Width:    float64(box.Rect.Right - box.Rect.Left),
			FontSize: math.Round(effective*1e4) / 1e4,
			Text:     char,
		})
	}
	return glyphs, nil
}
