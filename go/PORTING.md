# Frankfurter in Go

Everything Go lives under `go/`, module `github.com/lineofflight/frankfurter/go`. While both apps run, the Ruby app
(repository root) is the reference implementation: Go must produce the same data. This guide covers the layout, the
tooling, and the recipe for porting or changing an adapter; `docs/core-*.md` document the rest of the app area by
area. The exemplar adapters (`banrep` JSON, `bnr` XML, `rbv` HTML, `jpc` PDF) follow it exactly.

Same data means same rows (date, base, quote and every other field), rates equal within relative 1e-9, and the same
validation, blending and rollup outcomes. Float formatting, JSON key order and error wording may differ.

## Layout

```
go/
  PORTING.md                this file
  cmd/frankfurter           the binary: serve, schedule, migrate, backfill and the other tasks
  cmd/vcrconvert            Ruby VCR cassettes -> go-vcr cassettes
  cmd/genadapters           writes internal/adapters/all/all.go
  scripts/golden.rb         records Ruby adapter output for parity tests
  scripts/commentwrap       wraps comments at 80 columns (CI runs it with -l)
  testdata/cassettes/       converted cassettes, same base names as spec/vcr_cassettes (banrep.yml -> banrep.yaml)
  docs/core-<step>.md       one per core step (see the last section)
  internal/
    adapter/                Adapter interface, Rate, Base (HTTP, clock, sleep), helpers, registry
    adapters/<key>/         ONE PACKAGE PER ADAPTER, package name = lower-case provider key
    adapters/all/           generated blank imports of every adapter package
    db/, dbtest/            SQLite: open, embedded schema, fresh test databases
    deps/                   blank imports pinning every third-party module
    golden/                 parity check against scripts/golden.rb output
    pdftext/                PDF text and positioned runs, as pdf-reader gives them
    vcrtest/                cassette replay for tests
    xls/                    legacy .xls with the spreadsheet gem's cell typing
```

## Tooling

`go/mise.toml` pins the Go version (`mise install`). Run from `go/`:

```sh
gofmt -l .                                # must print nothing
go run ./scripts/commentwrap -l .         # must print nothing
go vet ./...
go test ./...
```

Ruby runs from the repository root, always with `APP_ENV=test` (without it, spec helpers wipe the developer database).
Replaying cassettes needs no network. `go/scripts/golden.rb` is usually all the Ruby you need; to run one spec file:

```sh
APP_ENV=test mise exec -- bundle exec ruby spec/provider/adapters/xyz_spec.rb
```

## Adapter recipe

For provider key `XYZ` (lower case `xyz`):

1. **Read** `lib/provider/adapters/xyz.rb`, `spec/provider/adapters/xyz_spec.rb`, and every cassette the spec inserts
   (`spec/vcr_cassettes/<name>.yml`; the Go copy is `go/testdata/cassettes/<name>.yaml`). Note each spec's
   `match_requests_on`, `allow_playback_repeats` and any `Date.stub(:today, ...)`.

2. **Record golden files**, one per `it` that fetches through a cassette with distinct arguments (at least one; the
   exemplars have one to four). From the repository root:

   ```sh
   mkdir -p go/internal/adapters/xyz/testdata/golden
   APP_ENV=test mise exec -- bundle exec ruby go/scripts/golden.rb xyz xyz method,uri \
     'fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24))' \
     > go/internal/adapters/xyz/testdata/golden/fetch.json
   ```

   Arguments: adapter key, cassette name, `match_requests_on` joined by commas (`method,host`, `method,uri`,
   `method,host,path`, `method,uri,body`), and the Ruby call evaluated on a new adapter. Flags go before the arguments:
   `--repeats` for `allow_playback_repeats: true`, `--today YYYY-MM-DD` for a stubbed `Date.today`. The file records all
   of it, so the Go test replays with identical matching. Never edit a golden file by hand; rerun the command.

3. **Write the adapter** in `go/internal/adapters/xyz/xyz.go` (skeleton below).

4. **Port every `it` block** to a Go test in `xyz_test.go` (package `xyz`, so tests reach unexported `parse`). One Go
   test per `it`, named after it (`it "skips records with missing values"` -> `TestParseSkipsMissingValues`); use a table
   when several `it`s differ only in data. Keep each assertion. A spec that inserts a cassette in `before` gets a fresh
   `vcrtest.Client` per test, as each Ruby `it` gets a fresh cassette.

5. **Add the golden test**: load each golden file, fetch with the same arguments, `g.Check(t, rates)`.

6. **Register** a new adapter with `go generate ./internal/adapters/all`.

7. **Check**: the commands under Tooling, all clean.

### Skeleton

```go
// Package xyz fetches rates from <institution>, which publishes <what, against which currency, how often>.
//
// <Carry over the Ruby class comment where it explains the source's quirks.>
package xyz

import (
	"context"
	"net/http"
	"time"

	"github.com/lineofflight/frankfurter/go/internal/adapter"
)

const baseURL = "https://example.org/rates.json"

func init() {
	adapter.Register("XYZ", func(c *http.Client) adapter.Adapter { return New(c) })
}

// Adapter fetches XYZ rates.
type Adapter struct {
	adapter.Base
}

// New returns an adapter that makes its requests with client.
func New(client *http.Client) *Adapter {
	return &Adapter{adapter.NewBase(client)}
}

// Fetch implements adapter.Adapter.
func (a *Adapter) Fetch(ctx context.Context, after, upto time.Time) ([]adapter.Rate, error) {
	body, err := a.Get(ctx, baseURL, nil)
	if err != nil {
		return nil, err
	}
	rates, err := parse(body)
	if err != nil {
		return nil, err
	}
	return adapter.Window(rates, after, upto), nil
}

func parse(data []byte) ([]adapter.Rate, error) { ... }
```

Override only what the Ruby class overrides:

| Ruby                              | Go                                                        |
|-----------------------------------|-----------------------------------------------------------|
| `def self.backfill_range = 365`   | `func (a *Adapter) BackfillRange() int { return 365 }`   |
| `def self.lead_days = 7`          | `func (a *Adapter) LeadDays() int { return 7 }`           |
| `def self.revises? = true`        | `func (a *Adapter) Revises() bool { return true }`        |
| `PREDECESSORS = { "AZN" => ["AZM", Date.new(2006, 1, 1)] }` | `var predecessors = map[string]adapter.Predecessor{"AZN": {Code: "AZM", Cutover: adapter.Date(2006, 1, 1)}}` then `adapter.HistoricalCode(predecessors, code, date)` |
| `SUCCESSORS = { "AFA" => ["AFN", Date.new(2003, 1, 7)] }` | `var successors = map[string]adapter.Successor{"AFA": {Code: "AFN", Cutover: adapter.Date(2003, 1, 7)}}` then `adapter.SuccessorCode(successors, code, date)` |
| `GRAMS_PER_TROY_OUNCE`            | `adapter.GramsPerTroyOunce`                               |
| `midpoint(buy, sell)`             | `adapter.Midpoint(buy, sell)`                             |
| `**prices(bid: b, ask: a)`        | `Bid: adapter.Float(b), Ask: adapter.Float(a)`            |
| `**prices(bid: b, ask: a, mid: m, unit: u)` | `Bid: adapter.Float(adapter.PerUnit(b, u))`, same for `Ask`, `Mid` |
| `Date.today`                      | `a.Today()` (tests pin `a.Now`)                           |
| `sleep(0.5)`                      | `if err := a.Sleep(ctx, 500*time.Millisecond); err != nil { return nil, err }` (no-op under `go test`) |
| `ENV["XYZ_API_KEY"] \|\| raise`   | `os.Getenv("XYZ_API_KEY")`, error when empty              |

### The fetch contract

- `Fetch(ctx, after, upto)` returns rows dated after `after` (exclusive) through `upto` (inclusive). A zero
  `time.Time` is Ruby's `nil`: an open bound. Where a Ruby adapter clips differently (JPC treats `after` as inclusive),
  keep the Ruby behaviour and say so in the doc comment.
- Dates are `time.Time` at UTC midnight. Build them with `adapter.Date(y, m, d)`; compare with `Equal`, `Before`,
  `After`, never `==`.
- `adapter.Window(rates, after, upto)` is Ruby's common `select { |r| r[:date] > after && r[:date] <= upto }`.
- A Ruby `raise` becomes a returned error. Don't prefix messages with the provider key; the caller adds it. Per-row
  skips (`next`, `filter_map` returning nil) stay skips. A Ruby `Float(x)` that would raise becomes an error; a
  `Float(x, exception: false)` that yields nil becomes a skip.
- Rows keep the Ruby order where a spec checks it (RBV: newest first).
- `adapter.Rate.Rate` is a `float64`. Components (`Bid`, `Ask`, `Mid`) are `*float64`, nil when Ruby's is nil. A zero
  component is a real value (BOJA publishes zero bids).

### HTTP

Embed `adapter.Base` and use its methods; never build your own client. The client passed to `New` is the recorder in
tests and `adapter.NewClient()` in production.

- `a.Get(ctx, url, url.Values{...})` is `http.get(url, params: {...}).to_s`.
- `a.PostForm(ctx, url, form)` is `http.post(url, form:).to_s`.
- Anything else (custom headers, JSON or XML bodies, reading response headers, tolerated statuses):

  ```go
  req, err := a.NewRequest(ctx, http.MethodPost, url, strings.NewReader(body))
  if err != nil { return nil, err }
  req.Header.Set("Content-Type", "application/json")
  resp, err := a.Do(req)            // a.Do(req, http.StatusNotFound) tolerates a 404, like ensure_success ignore: [404]
  if err != nil { return nil, err }
  cookie := adapter.CookieHeader(resp.Header) // Set-Cookie -> Cookie, as bi.rb and bota.rb do by hand
  ```

- Every request carries Frankfurter's User-Agent and `Accept: */*` (set by `NewRequest`); override on the request when
  Ruby does. Any status outside 2xx is an `*adapter.StatusError`, redirects included (they are not followed). 429s and
  connection errors retry up to five attempts, honouring Retry-After, as http.rb's retriable feature does.
- `url.Values.Encode` sorts keys and escapes `$` and spaces (`%24`, `+`) where http.rb does not. Cassette matching
  ignores that, and most servers do too. If a source needs Ruby's exact query string, set `req.URL.RawQuery` yourself.

### Values

- `adapter.ParseFloat(s)` is `Float(s, exception: false)`: trims whitespace, rejects empty, NaN, Inf.
  `strconv.ParseFloat` is fine where you have already cleaned the text. Delete thousands separators first
  (`strings.ReplaceAll(s, ",", "")`); for comma decimals swap them as the Ruby code does.
- Ruby `Date.parse` guesses; Go must name the layouts: `adapter.ParseDate(s, "2 January 2006", "02-Jan-06")`. Check
  which formats the cassette actually contains. `Date.strptime(s, "%Y%m%d")` is `time.Parse("20060102", s)`.
- `BigDecimal` arithmetic: use `adapter.Midpoint` and `adapter.PerUnit`, which compute in exact decimal (`math/big`) and
  round once. For anything else in decimal, `new(big.Rat).SetString(text)`; convert at the end with `Float64()`. Plain
  float division is right where Ruby divides floats (`rate / multiplier` on a Float).
- `String#to_i` on clean digits is `strconv.Atoi`; treat a parse error as Ruby's 0 only where that matters.

### Formats and libraries

| Format | Ruby            | Go                                                                                  |
|--------|-----------------|-------------------------------------------------------------------------------------|
| JSON   | json, oj        | `encoding/json`. Decode into structs; pointer fields (`*string`) where Ruby checks for nil; `json.Number` or `any` for fields that are sometimes numbers, sometimes strings. |
| XML    | ox, nokogiri    | `encoding/xml`. Struct tags with `a>b>c` paths mirror `locate("A/B/C")`; tags without a namespace match any namespace. For large or irregular documents use `xml.Decoder.Token`. Non-UTF-8 XML: set `decoder.CharsetReader = charset.NewReaderLabel` (`golang.org/x/net/html/charset`). |
| CSV    | csv             | `encoding/csv` with `FieldsPerRecord = -1` and `LazyQuotes = true` when the source is sloppy; strip a UTF-8 BOM (`bytes.TrimPrefix(b, []byte("﻿"))`) before parsing headers. |
| HTML   | nokogiri        | `github.com/PuerkitoBio/goquery`. `doc.css(sel)` -> `doc.Find(sel)`, `at_css` -> `.Find(sel).First()`, `.text` -> `.Text()`, `[attr]` -> `.Attr("attr")`. |
| XLSX   | rubyzip + ox    | Mirror the Ruby approach: if it reads the sheet XML itself, use `archive/zip` + `encoding/xml` the same way; otherwise `github.com/xuri/excelize/v2` with `excelize.Options{RawCellValue: true}` so you see stored values, not display strings. |
| XLS    | spreadsheet     | `internal/xls`, a small BIFF8 reader on `github.com/richardlehane/mscfb`: `sheets, err := xls.Open(body)`; `row.At(i)` gives a `Cell` whose `Kind` is `xls.Number`, `xls.String`, `xls.Date`, ... exactly where the gem returns Numeric, String, Date/DateTime. `cell.Text()` is `cell.to_s`. |
| PDF    | pdf-reader      | `internal/pdftext`: `pdftext.Pages(body)` gives each page's `Runs` (`page.runs`: X, Y, Width, FontSize, Text) and `Text()` (`page.text`); `pdftext.Text(body)` is `pages.map(&:text).join("\n")`. |
| ZIP    | rubyzip         | `archive/zip`: `zip.NewReader(bytes.NewReader(body), int64(len(body)))`.            |
| Legacy encodings | `force_encoding(...).encode("UTF-8")` | `golang.org/x/text/encoding/charmap` (`charmap.Windows1251.NewDecoder().Bytes(b)`, `charmap.ISO8859_1`). `force_encoding("UTF-8")` alone is a no-op in Go; `scrub`/`invalid: :replace` is `strings.ToValidUTF8(s, "�")`. |

Known extraction differences, harmless so far: PDFium decodes old Japanese fonts pdf-reader garbles, reads a
non-breaking space as a plain space, and its glyph widths can differ from pdf-reader's by a fraction of a point. The
.xls reader returns formula cells as empty and assumes the 1900 date system.

### Tests

```go
client := vcrtest.Client(t, "xyz", vcrtest.MatchOn(vcrtest.Method, vcrtest.Host), vcrtest.AllowPlaybackRepeats)
```

- `match_requests_on: [:method, :uri]` (VCR's default, also Go's default) -> `vcrtest.MatchOn(vcrtest.Method,
  vcrtest.URI)`; `[:method, :host]`, `[:method, :host, :path]`, `[:method, :uri, :body]` map one to one.
  `vcrtest.URI` compares query parameters as sets, so ordering and escaping don't matter. `vcrtest.Body` accepts equal
  form fields or equal JSON.
- An interaction plays once unless `AllowPlaybackRepeats`; a request nothing matches fails with the cassette name and
  the URL, which usually means your request differs from Ruby's.
- Cassettes recorded with credentials hold placeholders (`<FRED_API_KEY>`). Call `vcrtest.SetSecrets(t)` first; it sets
  each unset credential variable to its placeholder, so the adapter builds the recorded request. Ruby skips these specs
  without the variable; Go runs them. `SetSecrets` and `t.Setenv` rule out `t.Parallel`.
- `Date.stub(:today, d)` -> `a.Now = func() time.Time { return <d at noon UTC> }`.
- A spec's inline fixture (HTML, JSON, XML string) becomes a Go raw string literal passed to `parse`.
- `must_be_close_to(x, delta)` -> `math.Abs(got-x) > delta`; `must_equal` on floats -> `==`, as Ruby compares exactly.
- Specs calling core code (`RateValidation.reject!`, `Provider["X"].blends?`) can't call Go core packages that may not
  exist yet: assert the same outcome directly (see `jpc_test.go`).

Golden test, as in every exemplar:

```go
func TestGolden(t *testing.T) {
	g := golden.Load(t, "testdata/golden/fetch.json")
	a := New(g.Client(t))
	a.Now = g.Now(t) // only when recorded with --today
	rates, err := a.Fetch(context.Background(), adapter.Date(2026, 3, 16), adapter.Date(2026, 3, 24))
	if err != nil {
		t.Fatal(err)
	}
	g.Check(t, rates)
}
```

`Check` pairs rows by every non-rate field, compares `rate`, `bid`, `ask`, `mid` within 1e-9 relative, ignores order,
and prints missing, extra and differing rows.

## Rules

- One package per adapter. Helpers shared by several adapters belong in `internal/adapter`; one adapter's quirks stay in
  its own package.
- New third-party modules are pinned in `internal/deps` with a comment saying what they're for.
- No `t.Skip` to dodge a failure, no loosened assertions, no hand-edited golden files. A golden mismatch is a bug in the
  port until proven otherwise.

## The rest of the app

The same rules apply to the core packages (provider seeding and backfill, validation, precision, rollups, blending,
currency catalogue, cache, API, scheduler), plus:

- **Docs.** Each area documents its packages in `go/docs/core-<area>.md`: exported API, which Ruby files and specs they
  port, and deliberate deviations.
- **Errors.** Return errors, wrapped with context (`fmt.Errorf("refresh rollups: %w", err)`). Panic only on programmer
  error. Where Ruby rescues and logs (`Provider#backfill`), log and continue in the same place.
- **Logging.** `log/slog` with key-value attributes (`slog.Info("inserted rates", "provider", key, "count", n)`). No
  `fmt.Print` or `log.Print` in library code.
- **Context.** Every function that does I/O takes `ctx context.Context` first.
- **Database.** `internal/db` opens SQLite with the Ruby app's pragmas and holds the schema dumped from the migrated
  Ruby database; `dbtest.New(t)` gives each test its own fresh database. Dates are stored as `YYYY-MM-DD` text: bind
  `db.FormatDate(t)`, scan `DATE` columns into `time.Time` (the driver parses them) or select them through a function
  (`max(date)`) to get text. Inserts into `rates` use `INSERT ... ON CONFLICT DO NOTHING`, as Sequel's
  `insert_conflict`. Write rows through `mid`, `bid`, `ask`; `rate` is a generated column.
- **Adapters.** Look up by provider key with `adapter.Lookup(key)` after importing `internal/adapters/all`; walk windows
  with `adapter.FetchEach(ctx, a, after, today, yield)`. Adapters return raw rows: validation (`RateValidation`),
  precision (`RatePrecision.normalize`, 12 significant digits) and storage belong to core.
- **Cron.** `github.com/adhocore/gronx` parses the seeds' five-field `publish_schedule` and gives next and previous fire
  times (fugit's `next_time`/`previous_time`).
- **API.** `net/http` handlers. Validate responses in tests against `lib/public/v2/openapi.json` with
  `github.com/getkin/kin-openapi` (`openapi3filter.ValidateResponse`), as the Ruby specs do with skooma.
- **Test fixtures.** `internal/fixtures` ports `spec/fixtures.rb`, which generates data relative to today.
