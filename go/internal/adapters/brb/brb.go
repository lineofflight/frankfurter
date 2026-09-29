// Package brb fetches the Banque de la Republique du Burundi's daily reference rates against BIF for 19 currencies,
// published as one PDF bulletin per business day and listed on a paginated Drupal index. Each bulletin carries
// Acheteur (buy), Cours moyen jour (mid) and Vendeur (sell); we keep the mid.
//
// The site exposes only the paginated index, no date-range API. We walk the index, collect (date, PDF URL) pairs
// within the requested window, fetch each PDF and parse its fixed-layout table.
//
// Currency labels are French names. DTS is BRB's label for Special Drawing Rights and is emitted as XDR. Eleven
// currencies carry an asterisk meaning "not accepted by manual exchange bureaus"; it's informational only, so it is
// stripped from the label.
//
// Direction: foreign currency in base, BIF in quote (1 foreign = X BIF).
//
// TLS quirk: www.brb.bi omits its RapidSSL intermediate; see config/ca_bundles.
package brb

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
	"github.com/lineofflight/frankfurter/go/internal/pdftext"
)

const (
	host     = "https://www.brb.bi"
	indexURL = host + "/en/affichagetoustauxchange"
)

var (
	pdfHref    = regexp.MustCompile(`href="(/sites/default/files/\d{4}-\d{2}/Cours%20de%20change%20du%20(\d{2})-(\d{2})-(\d{4})[^"]*\.pdf)"`)
	rowPattern = regexp.MustCompile(`^1\s+(.+?)\*?\s+([\d.,]+)\s+([\d.,]+)\s+([\d.,]+)\s*$`)

	currencyNames = map[string]string{
		"Dollar Canadien":      "CAD",
		"Couronne Danoise":     "DKK",
		"Yen Japonais":         "JPY",
		"Couronne Norvegienne": "NOK",
		"Livre Sterling":       "GBP",
		"Couronne Suedoise":    "SEK",
		"Dollar USA":           "USD",
		"Franc Suisse":         "CHF",
		"Euro":                 "EUR",
		"Shilling Kenyan":      "KES",
		"DTS":                  "XDR",
		"Rand Sud-Africain":    "ZAR",
		"Dollar Australien":    "AUD",
		"Shilling Tanzanien":   "TZS",
		"Shilling Ougandais":   "UGX",
		"Franc Rwandais":       "RWF",
		"Yuan Renmimbi":        "CNY",
		"Dinar Kowetien":       "KWD",
		"Riyal Saoudien":       "SAR",
	}
)

func init() {
	adapter.Register("BRB", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BRB rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter. Chunk the archive so partial progress survives an unparseable PDF;
// discovery re-walks the (small) index per chunk.
func (a *Adapter) BackfillRange() int { return 30 }

type entry struct {
	date time.Time
	url  string
}

// Fetch implements adapter.Adapter. Like the Ruby adapter, `after` is inclusive: a bulletin dated after..upto is kept.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	entries, err := a.discover(ctx, after, upto)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	for i, e := range entries {
		if i > 0 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		body, err := a.Get(ctx, e.url, nil)
		if err != nil {
			return nil, err
		}
		// Some archive dates return a zero-byte body; treat the date as missing.
		if !bytes.HasPrefix(body, []byte("%PDF")) {
			continue
		}
		parsed, err := parse(body, e.date)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

func parse(pdf []byte, date time.Time) ([]adapter.Rate, error) {
	text, err := pdftext.Text(pdf)
	if err != nil {
		return nil, err
	}

	var rates []adapter.Rate
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		m := rowPattern.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		code, ok := currencyNames[strings.TrimSpace(m[1])]
		if !ok {
			continue
		}
		rate, err := strconv.ParseFloat(strings.ReplaceAll(m[3], ",", "."), 64)
		if err != nil {
			return nil, err
		}
		if rate == 0 {
			continue
		}
		rates = append(rates, adapter.Rate{Date: date, Base: code, Quote: "BIF", Rate: rate})
	}
	return rates, sc.Err()
}

func (a *Adapter) discover(ctx context.Context, after, upto time.Time) ([]entry, error) {
	var entries []entry
	for page := 0; ; page++ {
		if page > 0 {
			if err := a.Sleep(ctx, 500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		listed, err := a.indexPage(ctx, page)
		if err != nil {
			return nil, err
		}
		if len(listed) == 0 {
			break
		}

		oldest := listed[0].date
		for _, e := range listed {
			if e.date.Before(oldest) {
				oldest = e.date
			}
			if !after.IsZero() && e.date.Before(after) || e.date.After(upto) {
				continue
			}
			if !slices.ContainsFunc(entries, func(o entry) bool { return o.date.Equal(e.date) }) {
				entries = append(entries, e)
			}
		}
		if !after.IsZero() && oldest.Before(after) {
			break
		}
	}
	slices.SortFunc(entries, func(x, y entry) int { return x.date.Compare(y.date) })
	return entries, nil
}

// indexPage returns the bulletins listed on one index page, first link per date.
func (a *Adapter) indexPage(ctx context.Context, page int) ([]entry, error) {
	var params url.Values
	if page > 0 {
		params = url.Values{"page": {strconv.Itoa(page)}}
	}
	body, err := a.Get(ctx, indexURL, params)
	if err != nil {
		return nil, err
	}

	var entries []entry
	for _, m := range pdfHref.FindAllSubmatch(body, -1) {
		day, _ := strconv.Atoi(string(m[2]))
		month, _ := strconv.Atoi(string(m[3]))
		year, _ := strconv.Atoi(string(m[4]))
		date := adapter.Date(year, time.Month(month), day)
		if date.Day() != day || int(date.Month()) != month {
			return nil, fmt.Errorf("invalid date in bulletin link %s", m[1])
		}
		if !slices.ContainsFunc(entries, func(o entry) bool { return o.date.Equal(date) }) {
			entries = append(entries, entry{date, host + string(m[1])})
		}
	}
	return entries, nil
}
