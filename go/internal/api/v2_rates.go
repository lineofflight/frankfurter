package api

import (
	"bufio"
	"context"
	"database/sql"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/ratequery"
)

// rateQuery is what the rate routes need of a query; tests substitute their own.
type rateQuery interface {
	Range() bool
	DateRelative() bool
	ExpandProviders() bool
	CacheKey(ctx context.Context) (string, error)
	CSVFilename() string
	Each(ctx context.Context, yield func(ratequery.Record) error) error
	ReleaseSlot()
}

var newRateQuery = func(ctx context.Context, conn *sql.DB, params ratequery.Params, opts ratequery.Options) (rateQuery, error) {
	return ratequery.New(ctx, conn, params, opts)
}

// v2Now is the clock date-relative cache lifetimes count from.
var v2Now = time.Now

// cacheControlFor caps a date-relative query's lifetime at the next UTC midnight and drops stale-while-revalidate:
// such responses anchor on today, so they go stale at the rollover even when no new data arrives (and no purge fires),
// as when forward-dated provider rates enter scope (#541). The first request after midnight revalidates instead of
// being served yesterday's snapshot.
func cacheControlFor(q rateQuery) string {
	if !q.DateRelative() {
		return v2CacheControl
	}
	now := v2Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	seconds := int64(math.Ceil(midnight.Sub(now).Seconds()))
	return "public, max-age=" + strconv.FormatInt(seconds, 10) + ", stale-if-error=86400"
}

func (c *v2Request) rates(params ratequery.Params) {
	if c.paramsErr != nil {
		c.fail(c.paramsErr)
		return
	}
	q, err := newRateQuery(c.ctx(), c.s.DB, params, c.options())
	if err != nil {
		c.fail(err)
		return
	}
	defer q.ReleaseSlot()
	c.w.Header().Set("Cache-Control", cacheControlFor(q))
	key, err := q.CacheKey(c.ctx())
	if err != nil {
		c.fail(err)
		return
	}
	if etag(c.w, c.r, key) {
		return
	}

	if c.requestedType() == "csv" {
		c.w.Header().Set("Content-Type", contentTypeCSV)
		// Browsers ignore <a download> on cross-origin links, so the header is what makes a linked CSV download.
		c.w.Header().Set("Content-Disposition", `attachment; filename="`+q.CSVFilename()+`"`)
		headers := csvHeaders(q)
		line := func(r ratequery.Record) ([]byte, error) { return csvRecord(headers, r), nil }
		if q.Range() {
			c.stream(q, contentTypeCSV, csvLine(headers), "", "", line)
			return
		}
		records, err := collect(c.ctx(), q)
		if err != nil {
			c.fail(err)
			return
		}
		c.w.WriteHeader(http.StatusOK)
		if len(records) == 0 {
			return
		}
		var out strings.Builder
		out.WriteString(csvLine(headers))
		for _, r := range records {
			out.Write(csvRecord(headers, r))
		}
		c.w.Write([]byte(out.String()))
		return
	}

	switch {
	case strings.Contains(acceptHeader(c.r), contentTypeNDJSON):
		c.w.Header().Set("Vary", "Accept")
		c.stream(q, contentTypeNDJSON, "", "", "", func(r ratequery.Record) ([]byte, error) {
			b, err := r.MarshalJSON() // unescaped, as Oj writes it
			return append(b, '\n'), err
		})
	case q.Range():
		c.stream(q, contentTypeV2, "[", ",", "]", func(r ratequery.Record) ([]byte, error) { return r.MarshalJSON() })
	default:
		records, err := collect(c.ctx(), q)
		if err != nil {
			c.fail(err)
			return
		}
		writeJSON(c.w, http.StatusOK, contentTypeV2, records)
	}
}

func collect(ctx context.Context, q rateQuery) ([]ratequery.Record, error) {
	out := []ratequery.Record{}
	err := q.Each(ctx, func(r ratequery.Record) error {
		out = append(out, r)
		return nil
	})
	return out, err
}

// stream writes the query's records as they come: open, the records separated by sep, then close. Nothing is sent
// before the first record, so a failure computing it (a validation-style data error, an expired deadline) is an
// ordinary error response with its own headers, not a truncated 200. A failure after it aborts the connection, as Puma
// does when a streamed body raises.
func (c *v2Request) stream(q rateQuery, contentType, open, sep, close string, line func(ratequery.Record) ([]byte, error)) {
	var out *bufio.Writer
	begin := func() {
		c.w.Header().Set("Content-Type", contentType)
		c.w.WriteHeader(http.StatusOK)
		out = bufio.NewWriterSize(c.w, 4<<10)
		out.WriteString(open)
	}
	err := q.Each(c.ctx(), func(r ratequery.Record) error {
		b, err := line(r)
		if err != nil {
			return err
		}
		if out == nil {
			begin()
		} else {
			out.WriteString(sep)
		}
		_, err = out.Write(b)
		return err
	})
	if err != nil {
		if out == nil {
			c.fail(err)
			return
		}
		panic(http.ErrAbortHandler)
	}
	if out == nil {
		begin()
	}
	out.WriteString(close)
	out.Flush()
}

func (c *v2Request) rate(params ratequery.Params, base, quote string) {
	if c.paramsErr != nil {
		c.fail(c.paramsErr)
		return
	}
	merged := ratequery.Params{"base": strings.ToUpper(base), "quotes": strings.ToUpper(quote)}
	for k, v := range params {
		if k != "base" && k != "quotes" {
			merged[k] = v
		}
	}
	q, err := newRateQuery(c.ctx(), c.s.DB, merged, c.options())
	if err != nil {
		c.fail(err)
		return
	}
	defer q.ReleaseSlot()
	c.w.Header().Set("Cache-Control", cacheControlFor(q))
	records, err := collect(c.ctx(), q)
	if err != nil {
		c.fail(err)
		return
	}
	if len(records) == 0 {
		c.notFound()
		return
	}
	writeJSON(c.w, http.StatusOK, contentTypeV2, records[0])
}

func csvHeaders(q rateQuery) []string {
	headers := []string{"date", "base", "quote", "rate"}
	if q.ExpandProviders() {
		headers = append(headers, "providers")
	}
	return headers
}

// csvRecord is a record's CSV line. Numbers print as Ruby prints them (1.0, 1.2e-05, 12345); providers as
// KEY:RATE separated by pipes, with a trailing * on excluded ones.
func csvRecord(headers []string, r ratequery.Record) []byte {
	fields := make([]*string, len(headers))
	for i, h := range headers {
		var v string
		switch h {
		case "date":
			v = r.Date
		case "base":
			v = r.Base
		case "quote":
			v = r.Quote
		case "rate":
			v = r.Rate.String()
		case "providers":
			if !r.HasProviders {
				continue // nil: an empty, unquoted field
			}
			parts := make([]string, len(r.Providers))
			for j, p := range r.Providers {
				parts[j] = p.Key + ":" + p.Rate.String()
				if p.Excluded {
					parts[j] += "*"
				}
			}
			v = strings.Join(parts, "|")
		}
		fields[i] = &v
	}
	return []byte(csvFields(fields))
}

func csvLine(values []string) string {
	fields := make([]*string, len(values))
	for i := range values {
		fields[i] = &values[i]
	}
	return csvFields(fields)
}

// csvFields is Ruby's CSV.generate_line: a field is quoted when it holds a comma, a quote or a line break, or is an
// empty string (nil stays an empty, unquoted field).
func csvFields(fields []*string) string {
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		if f == nil {
			continue
		}
		if *f == "" || strings.ContainsAny(*f, ",\"\r\n") {
			b.WriteString(`"` + strings.ReplaceAll(*f, `"`, `""`) + `"`)
		} else {
			b.WriteString(*f)
		}
	}
	b.WriteByte('\n')
	return b.String()
}
