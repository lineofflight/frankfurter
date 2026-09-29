// Package bm fetches Banco de Moçambique's daily reference rates against MZN for 19 currencies, published as one PDF
// bulletin (~20 KB) per business day. USD/MZN is the bank's own 15:30 fixing from commercial-bank reporting; every
// other row is that fixing crossed with the Reuters USD rate for the currency (issue #386).
//
// There is no rate API. The Mercado Cambial page links one listing page per multi-year period
// (/pt/tabelas-de-taxas-de-cambio-de-referencia-diarias/2026-2025/ and so on back to 2018), and each listing links
// every bulletin in its period under an opaque media slug. We read the period slugs off the index, fetch only the
// listings whose years overlap the requested window, collect (date, pdf_url) pairs from the DDMMYYYY suffix of each
// filename, then fetch and parse each PDF.
//
// The bulletin is a fixed-layout text table: country, currency, buy (COMPRA), sell (VENDA) and, since November 2020,
// a published mid (MÉDIA). Rows are keyed on the country label because the currency label is unreliable: "Coroa" and
// "Kwacha" each appear more than once, and the USD row sometimes drops its label altogether. The published mid is
// emitted where present; older bulletins carry only buy and sell, so the mid is synthesised as the midpoint. Section
// headings state the unit ("Meticais por Unidade" or "por 1000 Unidades"), and the per-1000 block (JPY, MWK, TZS) is
// rescaled to per-unit.
//
// The Zimbabwe row is dropped: it has carried the same figure (about 167 MZN per 1000) since 2019, matching neither
// ZWL nor its 2024 successor ZWG, so no ISO code can be assigned honestly.
//
// Direction: foreign currency in base, MZN in quote (1 foreign = X MZN).
package bm

import (
	"context"
	"fmt"
	"html"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/pdftext"
)

const (
	host     = "https://www.bancomoc.mz"
	indexURL = host + "/pt/areas-de-actuacao/mercados/mercado-cambial/"

	// whitespace is what Ruby's String#strip removes.
	whitespace = " \t\n\v\f\r\x00"
)

var (
	listingHref = regexp.MustCompile(`href="(/pt/tabelas-de-taxas-de-cambio-de-referencia-diarias/(\d{4})-(\d{4})/)"`)
	pdfHref     = regexp.MustCompile(`href="(/media/[^"/]+/[^"]*?(\d{2})(\d{2})(\d{4})\.pdf)"`)

	// Section headings announce how many units of the foreign currency the row prices.
	unitsPattern = regexp.MustCompile(`(?i)Meticais por (?:(\d+) )?Unidade`)
	// Country and currency labels, then buy, sell and (optionally) mid. Labels start with a letter so numbered
	// section headings never match.
	rowPattern = regexp.MustCompile(`\A(\D.*?)\s+([\d.,]+)\s+([\d.,]+)(?:\s+([\d.,]+))?\z`)
	// Rates live in sections 1 and 2; section 3 carries prime rate, SOFR and gold, which must not be read as rows.
	endOfRates = regexp.MustCompile(`\A3\.\s+OUTRAS INFORMA`)

	footnote    = regexp.MustCompile(`\(\w\)`)
	columnBreak = regexp.MustCompile(`\s{2,}`)

	// Maps the country label in the bulletin to the ISO 4217 code of its currency. Older bulletins call eSwatini by
	// its former name.
	countries = map[string]string{
		"Estados Unidos": "USD",
		"Àfrica do Sul":  "ZAR",
		"Botswana":       "BWP",
		"eSwatini":       "SZL",
		"Swazilândia":    "SZL",
		"Mauricias":      "MUR",
		"Zâmbia":         "ZMW",
		"Japão":          "JPY",
		"Malawi":         "MWK",
		"Tanzânia":       "TZS",
		"Brasil":         "BRL",
		"Canada":         "CAD",
		"China/Offshore": "CNH",
		"China":          "CNY",
		"Dinamarca":      "DKK",
		"Inglaterra":     "GBP",
		"Noruega":        "NOK",
		"Suécia":         "SEK",
		"Suíça":          "CHF",
		"União Europeia": "EUR",
	}
)

func init() {
	adapter.Register("BM", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BM rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. Chunk the archive so partial progress survives an unparseable PDF;
// every chunk re-reads the index and the (large) listing page, so keep chunks wide.
func (a *Adapter) BackfillRange() int { return 60 }

type bulletin struct {
	date time.Time
	url  string
}

// Fetch implements adapter.Adapter. Unlike most adapters, `after` is inclusive: a bulletin dated `after` is kept.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	bulletins, err := a.discover(ctx, after, upto)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for i, b := range bulletins {
		if i > 0 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, b.url, nil)
		if err != nil {
			return nil, err
		}
		text, err := pdftext.Text(body)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(text, b.date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

func parse(text string, date time.Time) ([]adapter.Rate, error) {
	units := 1
	var rates []adapter.Rate
	for raw := range strings.SplitSeq(text, "\n") {
		line := strings.Trim(raw, whitespace)
		if endOfRates.MatchString(line) {
			break
		}

		if heading := unitsPattern.FindStringSubmatch(line); heading != nil {
			units = 1
			if heading[1] != "" {
				n, err := strconv.Atoi(heading[1])
				if err != nil {
					return nil, err
				}
				units = n
			}
			continue
		}

		row := rowPattern.FindStringSubmatch(line)
		if row == nil {
			continue
		}
		code, ok := countries[country(row[1])]
		if !ok {
			continue
		}

		buy, err := number(row[2])
		if err != nil {
			return nil, err
		}
		sell, err := number(row[3])
		if err != nil {
			return nil, err
		}
		var mid *float64
		var rate float64
		if row[4] != "" {
			m, err := number(row[4])
			if err != nil {
				return nil, err
			}
			mid = adapter.Float(adapter.PerUnit(m, float64(units)))
			rate = m
		} else {
			rate = adapter.Midpoint(buy, sell)
		}
		if units > 1 {
			rate /= float64(units)
		}
		if rate == 0 {
			continue
		}

		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  code,
			Quote: "MZN",
			Rate:  rate,
			Bid:   adapter.Float(adapter.PerUnit(buy, float64(units))),
			Ask:   adapter.Float(adapter.PerUnit(sell, float64(units))),
			Mid:   mid,
		})
	}
	return rates, nil
}

// country picks the country out of the label cell, which holds the country, a footnote marker like "(a)" on some
// rows, then the currency name after a run of spaces. Only the country identifies the row.
func country(label string) string {
	if loc := footnote.FindStringIndex(label); loc != nil {
		label = label[:loc[0]] + label[loc[1]:]
	}
	label = strings.Trim(label, whitespace)
	if label == "" {
		return ""
	}
	return columnBreak.Split(label, 2)[0]
}

// number reads a decimal-comma figure; a thousands dot never appears in a rate row but is harmless to drop.
func number(text string) (float64, error) {
	s := strings.ReplaceAll(strings.ReplaceAll(text, ".", ""), ",", ".")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", text)
	}
	return f, nil
}

func (a *Adapter) discover(ctx context.Context, after, upto time.Time) ([]bulletin, error) {
	urls, err := a.listings(ctx, after, upto)
	if err != nil {
		return nil, err
	}

	var bulletins []bulletin
	for i, u := range urls {
		if i > 0 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		listed, err := a.listing(ctx, u)
		if err != nil {
			return nil, err
		}
		for _, b := range listed {
			if !after.IsZero() && b.date.Before(after) || b.date.After(upto) {
				continue
			}
			if !slices.ContainsFunc(bulletins, func(o bulletin) bool { return o.date.Equal(b.date) }) {
				bulletins = append(bulletins, b)
			}
		}
	}
	slices.SortFunc(bulletins, func(x, y bulletin) int { return x.date.Compare(y.date) })
	return bulletins, nil
}

type period struct {
	first, last int
	url         string
}

// listings returns the listing pages whose years overlap the window. Listing pages are named for the years they span
// ("2026-2025", "2024-2022"). The newest is treated as open-ended so a window reaching into a year the bank has not
// yet split off still finds the current listing.
func (a *Adapter) listings(ctx context.Context, after, upto time.Time) ([]string, error) {
	body, err := a.Get(ctx, indexURL, nil)
	if err != nil {
		return nil, err
	}
	var periods []period
	for _, m := range listingHref.FindAllStringSubmatch(string(body), -1) {
		url := host + m[1]
		if slices.ContainsFunc(periods, func(p period) bool { return p.url == url }) {
			continue
		}
		first, _ := strconv.Atoi(m[2])
		second, _ := strconv.Atoi(m[3])
		periods = append(periods, period{min(first, second), max(first, second), url})
	}
	if len(periods) == 0 {
		return nil, fmt.Errorf("no listing pages on %s", indexURL)
	}
	slices.SortStableFunc(periods, func(x, y period) int { return x.first - y.first })
	periods[len(periods)-1].last = math.MaxInt

	var urls []string
	for _, p := range periods {
		if !after.IsZero() && after.Year() > p.last || upto.Year() < p.first {
			continue
		}
		urls = append(urls, p.url)
	}
	return urls, nil
}

func (a *Adapter) listing(ctx context.Context, listingURL string) ([]bulletin, error) {
	body, err := a.Get(ctx, listingURL, nil)
	if err != nil {
		return nil, err
	}
	var bulletins []bulletin
	for _, m := range pdfHref.FindAllStringSubmatch(string(body), -1) {
		year, _ := strconv.Atoi(m[4])
		month, _ := strconv.Atoi(m[3])
		day, _ := strconv.Atoi(m[2])
		date := adapter.Date(year, time.Month(month), day)
		if date.Year() != year || int(date.Month()) != month || date.Day() != day {
			return nil, fmt.Errorf("invalid date in %s", m[1])
		}
		bulletins = append(bulletins, bulletin{date, host + escape(html.UnescapeString(m[1]))})
	}
	return bulletins, nil
}

// escape percent-encodes every byte outside RFC 2396's unreserved and reserved sets, as Ruby's
// URI::RFC2396_PARSER.escape does.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'();/?:@&=+$,[]", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
