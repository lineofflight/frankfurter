// Package bi fetches rates from Bank Indonesia, which publishes daily transaction exchange rates for 26 currencies
// against the Indonesian rupiah (IDR).
//
// The page is SharePoint-based; we POST a search per currency and parse the HTML result table. Rates are buy/sell;
// the mid-rate is (sell + buy) / 2.
package bi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://www.bi.go.id/en/statistik/informasi-kurs/transaksi-bi/default.aspx"

var (
	tableRe  = regexp.MustCompile(`(?is)gvSearchResult2"[^>]*>(.*?)</table>`)
	rowRe    = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	cellRe   = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	tagRe    = regexp.MustCompile(`<[^>]+>`)
	prefixRe = regexp.MustCompile(`name="(ctl00\$PlaceHolderMain\$[^"]*)\$txtFrom"`)
	selectRe = regexp.MustCompile(`(?is)<select[^>]*ddlmatauang[^>]*>(.*?)</select>`)
	optionRe = regexp.MustCompile(`<option[^>]*value="([^"]*)"`)

	errNoTable = errors.New("neither results table nor search form in response")
)

func init() {
	adapter.Register("BI", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches BI rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// BackfillRange implements adapter.Adapter.
func (a *Adapter) BackfillRange() int { return 90 }

// Fetch implements adapter.Adapter. As in Ruby, the search results are returned as is, not clipped to the window, and
// after must be set.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	if upto.IsZero() {
		upto = a.Today()
	}
	from, to := after.Format("02-Jan-2006"), upto.Format("02-Jan-2006")

	req, err := a.NewRequest(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Do(req)
	if err != nil {
		return nil, err
	}
	cookies := adapter.CookieHeader(resp.Header)
	page := string(resp.Body)

	var prefix string
	if m := prefixRe.FindStringSubmatch(page); m != nil {
		prefix = m[1]
	}
	fields := sharepointFields(page)

	var rates []adapter.Rate
	for i, currency := range currencies(page) {
		if i > 0 {
			if err := a.Sleep(ctx, time.Second); err != nil {
				return nil, err
			}
		}
		html, err := a.search(ctx, fields, prefix, cookies, currency, from, to)
		if err != nil {
			return nil, err
		}
		parsed, err := parse(html, currency)
		if err != nil {
			return nil, err
		}
		rates = append(rates, parsed...)
	}
	return rates, nil
}

func (a *Adapter) search(ctx context.Context, fields url.Values, prefix, cookies, currency, from, to string) (string, error) {
	form := url.Values{}
	for k, v := range fields {
		form[k] = v
	}
	form.Set(prefix+"$ddlmatauang1", currency)
	form.Set(prefix+"$txtFrom", from)
	form.Set(prefix+"$txtTo", to)
	form.Set(prefix+"$txtTanggal", "")
	form.Set(prefix+"$btnSearch1", "Search")
	form.Set(prefix+"$hidSourceID", strings.ReplaceAll(prefix, "$", "_")+"_btnSearch1")

	req, err := a.NewRequest(ctx, http.MethodPost, baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookies)
	req.Header.Set("Referer", baseURL)
	resp, err := a.Do(req)
	if err != nil {
		return "", err
	}
	return string(resp.Body), nil
}

func parse(html, currency string) ([]adapter.Rate, error) {
	base := strings.TrimSpace(currency)
	m := tableRe.FindStringSubmatch(html)
	if m == nil {
		// No-result searches re-render the page without a gvSearchResult2 table (observed for weekend-only windows);
		// the search web part is still present in those re-renders.
		if strings.Contains(html, "btnSearch1") {
			return nil, nil
		}
		return nil, fmt.Errorf("%w for %s", errNoTable, base)
	}

	var rates []adapter.Rate
	for _, row := range rowRe.FindAllStringSubmatch(m[1], -1) {
		var cells []string
		for _, c := range cellRe.FindAllStringSubmatch(row[1], -1) {
			cells = append(cells, strings.TrimSpace(tagRe.ReplaceAllString(strings.TrimSpace(c[1]), "")))
		}
		if len(cells) < 4 {
			continue
		}
		sell, okSell := adapter.ParseFloat(strings.ReplaceAll(cells[1], ",", ""))
		buy, okBuy := adapter.ParseFloat(strings.ReplaceAll(cells[2], ",", ""))
		if !okSell || !okBuy || sell <= 0 || buy <= 0 {
			continue
		}
		date, err := adapter.ParseDate(cells[3], "2 Jan 2006", "2 January 2006")
		if err != nil {
			return nil, err // Ruby's Date.parse raises
		}
		rates = append(rates, adapter.Rate{
			Date:  date,
			Base:  base,
			Quote: "IDR",
			Rate:  adapter.Midpoint(sell, buy),
			Bid:   adapter.Float(buy),
			Ask:   adapter.Float(sell),
		})
	}
	return rates, nil
}

func currencies(html string) []string {
	var codes []string
	for _, sel := range selectRe.FindAllStringSubmatch(html, -1) {
		for _, opt := range optionRe.FindAllStringSubmatch(sel[1], -1) {
			codes = append(codes, opt[1])
		}
	}
	return codes
}

func token(html, name string) string {
	re := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `"[^>]*value="([^"]*)"`)
	if m := re.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return ""
}

func sharepointFields(page string) url.Values {
	return url.Values{
		"MSOWebPartPage_PostbackSource":               {""},
		"MSOTlPn_View":                                {"0"},
		"MSOTlPn_ShowSettings":                        {"False"},
		"MSOTlPn_Button":                              {"none"},
		"__EVENTTARGET":                               {""},
		"__EVENTARGUMENT":                             {""},
		"__REQUESTDIGEST":                             {token(page, "__REQUESTDIGEST")},
		"MSOSPWebPartManager_DisplayModeName":         {"Browse"},
		"MSOSPWebPartManager_ExitingDesignMode":       {"false"},
		"MSOSPWebPartManager_OldDisplayModeName":      {"Browse"},
		"MSOSPWebPartManager_StartWebPartEditingName": {"false"},
		"MSOSPWebPartManager_EndWebPartEditing":       {"false"},
		"_maintainWorkspaceScrollPosition":            {"0"},
		"__VIEWSTATE":                                 {token(page, "__VIEWSTATE")},
		"__VIEWSTATEGENERATOR":                        {token(page, "__VIEWSTATEGENERATOR")},
		"__SCROLLPOSITIONX":                           {"0"},
		"__SCROLLPOSITIONY":                           {"0"},
		"__EVENTVALIDATION":                           {token(page, "__EVENTVALIDATION")},
	}
}
