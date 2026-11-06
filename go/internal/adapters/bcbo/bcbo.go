// Package bcbo fetches Banco Central de Bolivia's daily official exchange rates
// of the boliviano (BOB) against the US dollar and about 50 other currencies,
// plus daily reference prices for gold, silver and the SDR.
//
// The USD/BOB rate held a stabilized peg (VENTA 6.96 / COMPRA 6.86, mid 6.91)
// from 2011 until mid-2026, when Bolivia repriced the boliviano and the source
// replaced its daily-sheet layout. The single official rate (TCO) is now
// published directly.
//
// Sources:
//  1. Yearly archives (USD/BOB only) at tiposDeCambioHistorico/xls.php?anio=YYYY, used before 2008 (coverage starts
//     2000-01-01).
//  2. Daily multi-currency spreadsheets at librerias/indicadores/otras/otras_imprimir2XLS.php?qdd=DD&qmm=MM&qaa=YYYY,
//     used for all currencies and metals from 2008-01-01 onwards.
//
// The daily sheet exists in two layouts (0-based columns):
//   - Legacy (through ~2026-06): currency marker in column 3, rate in column 4 (5 for SDR), USD split across
//     USD.VENTA / USD.COMPRA rows averaged to a mid.
//   - Current (2026-07 onward): ISO code in column 2, rate in column 3, USD carried as a single official rate (TCO),
//     metals and SDR in their own labelled blocks.
//
// Rates keep BCBO's direction: foreign currency as base, BOB as quote. Gold
// (XAU), silver (XAG) and the SDR (XDR) are quoted against USD.
package bcbo

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/xls"
)

const (
	host     = "https://www.bcb.gob.bo"
	yearURL  = host + "/tiposDeCambioHistorico/xls.php"
	dailyURL = host + "/librerias/indicadores/otras/otras_imprimir2XLS.php"
)

var (
	coverageStart = adapter.Date(2000, time.January, 1)
	dailyStart    = adapter.Date(2008, time.January, 1)

	isoCode = regexp.MustCompile(`^[A-Z]{3}$`)
	sdr     = regexp.MustCompile(`(?i)DERECHO ESPECIAL DE GIRO`)
)

func init() {
	adapter.Register("BCBO", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BCBO rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. Daily queries are used from 2008
// onwards, so a small range keeps progress durable and avoids overloading the
// server.
func (a *Adapter) BackfillRange() int { return 30 }

// Fetch implements adapter.Adapter. As in the Ruby adapter, `after` is
// inclusive: the window is after..upto.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	start := coverageStart
	if after.After(start) {
		start = after
	}
	end := upto
	if end.IsZero() {
		end = a.Today()
	}
	if start.After(end) {
		return nil, nil
	}

	var rates []adapter.Rate

	yearlyEnd := dailyStart.AddDate(0, 0, -1)
	if end.Before(yearlyEnd) {
		yearlyEnd = end
	}
	if !start.After(yearlyEnd) {
		for year := start.Year(); year <= yearlyEnd.Year(); year++ {
			if year != start.Year() {
				if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
					return nil, err
				}
			}
			body, err := a.Get(ctx, yearURL, url.Values{"anio": {strconv.Itoa(year)}})
			if err != nil {
				return nil, err
			}
			records, err := parseYearly(body, year)
			if err != nil {
				return nil, err
			}
			for _, r := range records {
				if !r.Date.Before(start) && !r.Date.After(yearlyEnd) {
					rates = append(rates, r)
				}
			}
		}
	}

	dailyFrom := start
	if dailyFrom.Before(dailyStart) {
		dailyFrom = dailyStart
	}
	first := true
	for date := dailyFrom; !date.After(end); date = date.AddDate(0, 0, 1) {
		if wd := date.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if !first {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		first = false

		body, err := a.Get(ctx, dailyURL, url.Values{
			"qdd": {strconv.Itoa(date.Day())},
			"qmm": {strconv.Itoa(int(date.Month()))},
			"qaa": {strconv.Itoa(date.Year())},
		})
		if err != nil {
			return nil, err
		}
		records, err := parseDaily(body, date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, records...)
	}

	return rates, nil
}

func firstSheet(data []byte) (xls.Sheet, error) {
	sheets, err := xls.Open(data)
	if err != nil {
		return xls.Sheet{}, err
	}
	if len(sheets) == 0 {
		return xls.Sheet{}, errors.New("workbook has no worksheet")
	}
	return sheets[0], nil
}

// parseYearly reads a yearly archive: day of month in column 0, then a
// VENTA/COMPRA pair per month.
func parseYearly(data []byte, year int) ([]adapter.Rate, error) {
	sheet, err := firstSheet(data)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for _, row := range sheet.Rows {
		dayCell := row.At(0)
		if dayCell.Kind != xls.Number {
			continue
		}
		day := math.Trunc(dayCell.Number)
		if day < 1 || day > 31 {
			continue
		}

		for month := time.January; month <= time.December; month++ {
			sellColumn := (int(month)-1)*2 + 1
			sell, buy := row.At(sellColumn), row.At(sellColumn+1)
			if sell.Kind != xls.Number || buy.Kind != xls.Number {
				continue
			}
			if sell.Number == 0 || buy.Number == 0 {
				continue
			}
			date, ok := validDate(year, month, int(day))
			if !ok {
				continue
			}
			rates = append(rates, adapter.Rate{
				Date:  date,
				Base:  "USD",
				Quote: "BOB",
				Rate:  adapter.Midpoint(sell.Number, buy.Number),
				Bid:   adapter.Float(buy.Number),
				Ask:   adapter.Float(sell.Number),
			})
		}
	}
	return rates, nil
}

// parseDaily reads a daily sheet in either layout. The current layout carries
// ISO codes in column 2; the legacy layout leaves that column blank and puts
// its markers in column 3.
func parseDaily(data []byte, date time.Time) ([]adapter.Rate, error) {
	sheet, err := firstSheet(data)
	if err != nil {
		return nil, err
	}
	for _, row := range sheet.Rows {
		if isoCode.MatchString(text(row, 2)) {
			return parseDailyCurrent(sheet.Rows, date), nil
		}
	}
	return parseDailyLegacy(sheet.Rows, date), nil
}

func parseDailyCurrent(rows []xls.Row, date time.Time) []adapter.Rate {
	var rates []adapter.Rate
	add := func(base, quote string, cell xls.Cell) {
		if rate, ok := parseRate(cell); ok && rate > 0 {
			rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: quote, Rate: rate})
		}
	}

	for _, row := range rows {
		concept := text(row, 0)
		code := text(row, 2)

		switch {
		case isoCode.MatchString(code):
			add(code, "BOB", row.At(3))
		case sdr.MatchString(text(row, 1)):
			add("XDR", "USD", row.At(3))
		case strings.EqualFold(concept, "ORO"):
			add("XAU", "USD", row.At(3))
		case strings.EqualFold(concept, "PLATA"):
			add("XAG", "USD", row.At(3))
		}
	}
	return rates
}

func parseDailyLegacy(rows []xls.Row, date time.Time) []adapter.Rate {
	var rates []adapter.Rate
	var venta, compra float64

	for _, row := range rows {
		code := text(row, 3)
		switch code {
		case "":
			continue
		case "USD./O.T.F.":
			metal := strings.ToUpper(text(row, 0))
			var base string
			switch {
			case strings.Contains(metal, "ORO"):
				base = "XAU"
			case strings.Contains(metal, "PLATA"):
				base = "XAG"
			default:
				continue
			}
			if rate, ok := parseRate(row.At(4)); ok && rate > 0 {
				rates = append(rates, adapter.Rate{Date: date, Base: base, Quote: "USD", Rate: rate})
			}
		case "USD/D.E.G.":
			if rate, ok := parseRate(row.At(5)); ok && rate > 0 {
				rates = append(rates, adapter.Rate{Date: date, Base: "XDR", Quote: "USD", Rate: rate})
			}
		case "USD.VENTA":
			if rate, ok := parseRate(row.At(4)); ok && rate > 0 {
				venta = rate
			}
		case "USD.COMPRA":
			if rate, ok := parseRate(row.At(4)); ok && rate > 0 {
				compra = rate
			}
		case "USD":
			// Ecuador's USD row would duplicate USD/BOB.
			continue
		default:
			if !isoCode.MatchString(code) {
				continue
			}
			if rate, ok := parseRate(row.At(4)); ok && rate > 0 {
				rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "BOB", Rate: rate})
			}
		}
	}

	if venta > 0 && compra > 0 {
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  "USD",
			Quote: "BOB",
			Rate:  adapter.Midpoint(compra, venta),
			Bid:   adapter.Float(compra),
			Ask:   adapter.Float(venta),
		})
	}
	return rates
}

func text(row xls.Row, i int) string {
	return strings.TrimSpace(row.At(i).Text())
}

func parseRate(cell xls.Cell) (float64, bool) {
	if cell.Kind == xls.Number {
		return cell.Number, true
	}
	return adapter.ParseFloat(strings.ReplaceAll(cell.Text(), ",", ""))
}

// validDate builds a date, rejecting ones time.Date would normalize (30
// February).
func validDate(year int, month time.Month, day int) (time.Time, bool) {
	d := adapter.Date(year, month, day)
	return d, d.Month() == month && d.Day() == day
}
