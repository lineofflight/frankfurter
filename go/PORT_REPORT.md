# Go port report

State of `go/` on branch `go-port` at 74f79a9e (278 commits ahead of `main`), checked on 2026-09-29.

## Verdict

The port is functionally complete. All 105 adapters, the domain, provider, blend, v1 and v2 API, migrations and ops
commands are in Go and agree with Ruby on everything I could compare: unit specs, adapter goldens recorded from Ruby, a
580-request API golden corpus, and 99 live-data requests against a production snapshot.

It isn't a drop-in replacement yet. The deploy path still runs Ruby, the Docker image has never been built, no Go
adapter has fetched from a live source, and nobody has measured performance. Run it in shadow before cutting over.

## What I verified for this report

- `gofmt -l .` prints nothing and `go vet ./...` passes. `go test -count=1 ./...` passes 1,895 top-level tests and 356
  subtests across 127 packages, with 0 failures and 0 skips.
- The Go tree has no `t.Skip` and no TODO or FIXME.
- I reran `scripts/smoke_parity.sh` on the 2026-09-13 production backup: 99 of 99 requests matched, and 57 v2 responses
  passed OpenAPI validation.
- I re-recorded 5 adapter goldens from Ruby and all matched the committed files: BCV and BDL (.xls), the JPC 2002 PDF
  archive, ECB (8,067 rows) and FRED.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` builds a static 25.0 MB binary. The ops step reported 21.5 MB, measured before
  every adapter was linked in.
- Every Ruby spec file is listed as ported in some `docs/core-*.md`, except the RuboCop cop spec.

## Per area

| Area | Ruby spec files | Ruby `it` | Go packages | Go tests (top / sub) |
|---|--:|--:|---|--:|
| Adapters | 105 | 814 | `internal/adapters/*`, `all` | 1,182 / 209 |
| Adapter base and foundation | 1 | 14 | `adapter`, `pdftext`, `xls`, `vcrtest`, `golden` | 34 / 23 |
| Domain | 14 | 144 | `rates`, `currency`, `seeds`, `fixtures`, `db`, `applog` | 146 / 32 |
| Provider | 5 | 85 | `provider`, `schedule`, `heavyslots`, `cmd/providerhealth` | 93 / 21 |
| Blend | 10 | 102 | `blend` | 91 / 30 |
| API (app, cache, timeout, v1, v2, parity) | 20 | 378 | `api`, `ratequery`, `cache` | 326 / 29 |
| Ops (migrations) | 7 | 15 | `migrate`, `cmd/frankfurter` | 23 / 12 |
| RuboCop cop | 1 | 7 | none (intentional) | none |
| **Total** | **163** | **1,559** | | **1,895 / 356** |

How to read the counts:

- Ruby `it` counts are static, so an `it` generated in a loop counts once.
- Go counts don't map 1:1 to Ruby. Table tests merge several `it`s into one test, while golden tests and extra
  Ruby-parity tests add more.
- Every adapter package has at least as many Go tests as its spec has `it`s. CBI looks short (21 vs 19), but its
  table tests cover all 21.
- The API has fewer Go tests than Ruby `it`s for the same reason. Its golden test also replays 580 requests as a
  single test. I spot-checked `rate_query_spec` by `it` name against `ratequery` and found no missing cases.

Only `spec/rubocop/cop/comment_fill_spec.rb` has no Go counterpart. Some cases were ported in changed form, and the
step docs list each one:

- Ruby stubs became unexported test seams.
- Child-process migration specs became one database file per test.
- rufus stubs became a fake registrar.

## Adapters

Each adapter has:

- its full spec ported;
- one or more golden files recorded by `scripts/golden.rb` from Ruby under the same cassette and request matching;
- rates compared within 1e-9 relative.

Across all adapters that is 150 golden files and 83,991 rows. All 105 adapters are registered in the binary, and
`all/seeded_test.go` checks that each of the 104 seeded providers resolves. BANGUAT (*) compiles but has no seed, as
in Ruby. The thinnest goldens are BSP (3 rows), BANREP (5) and CBLLR (6). CBBH's `empty.json` checks an empty fetch
on purpose.

| Adapter | Ruby / Go tests | Golden files / rows | Adapter | Ruby / Go tests | Golden files / rows | Adapter | Ruby / Go tests | Golden files / rows |
|---|--:|--:|---|--:|--:|---|--:|--:|
| AMCM | 8 / 18 | 1 / 85 | BOJA | 5 / 12 | 2 / 51 | HKMA | 6 / 10 | 1 / 68 |
| BAM | 8 / 14 | 1 / 118 | BOM | 8 / 11 | 1 / 152 | HMRC | 15 / 20 | 2 / 423 |
| BANGUAT* | 3 / 8 | 1 / 20 | BOMU | 11 / 18 | 2 / 46 | HNB | 4 / 12 | 1 / 65 |
| BANREP | 3 / 5 | 1 / 5 | BOT | 4 / 9 | 1 / 57 | IMF | 10 / 13 | 1 / 734 |
| BANXICO | 5 / 10 | 1 / 50 | BOTA | 8 / 13 | 1 / 43 | INFOREURO | 12 / 19 | 5 / 1734 |
| BBK | 7 / 13 | 2 / 216 | BOZ | 10 / 17 | 3 / 76 | JPC | 14 / 21 | 4 / 553 |
| BCB | 6 / 10 | 1 / 50 | BRB | 8 / 12 | 2 / 57 | LB | 8 / 13 | 1 / 264 |
| BCBO | 9 / 14 | 2 / 54 | BSP | 9 / 17 | 2 / 3 | MAS | 9 / 13 | 2 / 609 |
| BCC | 6 / 16 | 1 / 52 | CBA | 2 / 7 | 1 / 390 | MMA | 7 / 12 | 2 / 3558 |
| BCCCD | 10 / 13 | 1 / 42 | CBAR | 11 / 16 | 2 / 168 | MNB | 6 / 11 | 1 / 160 |
| BCCH | 6 / 9 | 1 / 80 | CBBH | 10 / 16 | 4 / 139 | NB | 6 / 12 | 1 / 266 |
| BCCR | 3 / 8 | 1 / 100 | CBC | 8 / 14 | 2 / 60 | NBC | 12 / 18 | 1 / 87 |
| BCEAO | 5 / 7 | 1 / 105 | CBE | 8 / 16 | 1 / 126 | NBE | 8 / 14 | 1 / 57 |
| BCP | 9 / 10 | 1 / 39 | CBG | 11 / 18 | 1 / 46 | NBG | 7 / 13 | 1 / 126 |
| BCRA | 10 / 22 | 1 / 165 | CBI | 21 / 19 | 2 / 1010 | NBK | 7 / 11 | 1 / 117 |
| BCT | 9 / 12 | 1 / 60 | CBK | 7 / 13 | 1 / 6384 | NBKR | 12 / 24 | 2 / 53 |
| BCU | 9 / 14 | 1 / 78 | CBKKW | 8 / 13 | 1 / 104 | NBM | 10 / 15 | 1 / 126 |
| BCV | 13 / 18 | 1 / 105 | CBLLR | 7 / 11 | 1 / 6 | NBP | 6 / 11 | 1 / 248 |
| BDI | 7 / 16 | 1 / 501 | CBM | 5 / 9 | 1 / 38 | NBRB | 2 / 7 | 1 / 378 |
| BDL | 10 / 13 | 2 / 105 | CBN | 10 / 17 | 2 / 72 | NBRM | 5 / 9 | 1 / 36 |
| BDP | 12 / 16 | 1 / 96 | CBO | 10 / 13 | 1 / 44 | NBT | 9 / 13 | 1 / 108 |
| BFM | 9 / 12 | 2 / 133 | CBR | 7 / 11 | 1 / 174 | NBU | 3 / 8 | 1 / 495 |
| BI | 6 / 11 | 1 / 40 | CBS | 10 / 17 | 2 / 171 | NRB | 5 / 11 | 1 / 110 |
| BIS | 12 / 20 | 4 / 423 | CBSL | 7 / 11 | 1 / 165 | NRBT | 7 / 14 | 2 / 168 |
| BM | 11 / 14 | 1 / 57 | CBSSC | 9 / 18 | 4 / 56 | PMA | 11 / 15 | 1 / 150 |
| BNA | 8 / 12 | 1 / 414 | CBTT | 8 / 13 | 1 / 35 | RB | 8 / 16 | 1 / 116 |
| BNM | 6 / 14 | 2 / 567 | CBU | 8 / 13 | 1 / 225 | RBA | 3 / 7 | 1 / 6166 |
| BNR | 6 / 7 | 1 / 74 | CBVS | 19 / 27 | 2 / 33 | RBF | 6 / 16 | 3 / 56 |
| BNRRW | 9 / 21 | 1 / 64 | CFETS | 10 / 16 | 2 / 1725 | RBM | 10 / 15 | 1 / 152 |
| BOA | 7 / 14 | 2 / 64 | CNB | 8 / 14 | 1 / 150 | RBV | 6 / 7 | 1 / 24 |
| BOB | 3 / 10 | 2 / 33655 | DAB | 10 / 15 | 1 / 30 | SARB | 6 / 14 | 1 / 92 |
| BOC | 2 / 7 | 1 / 115 | DNB | 7 / 15 | 1 / 8007 | SBI | 7 / 14 | 1 / 128 |
| BOE | 5 / 10 | 1 / 104 | ECB | 1 / 5 | 1 / 8067 | SBP | 8 / 13 | 3 / 87 |
| BOI | 5 / 9 | 1 / 28 | FBIL | 7 / 14 | 1 / 18 | TCMB | 3 / 8 | 1 / 294 |
| BOJ | 5 / 9 | 1 / 20 | FRED | 2 / 8 | 1 / 220 | UST | 10 / 16 | 1 / 151 |

Go counts include golden tests.

## Smoke parity

`scripts/smoke_parity.sh` compares the two apps on a real database:

- It clones a snapshot and runs Ruby's `rake db:setup` on the copy (migrating 32 to 40 and seeding).
- It starts Puma and `frankfurter serve` on the same copy, both with `TZ=UTC`.
- It sends the 99 requests in `scripts/smoke_corpus.txt` to both. The requests cover v1 and v2 latest, historical
  dates, weekends, ranges, bases, symbol and provider filters, currencies, providers, coverage, week and month rollups,
  pairs, CSV, NDJSON, Accept negotiation and errors.

Results:

- On the 2026-09-13 production backup (12.1M rates, blended tables filled), 99 of 99 requests matched and 57 of 57
  documented v2 responses passed OpenAPI validation.
- The comparer's number tolerance is 1e-6 relative, because API output is rounded. The 1e-9 bound is enforced in the
  unit and golden tests.

The only mismatch found was in the environment. Ruby evaluates `publishes_missed` cron schedules in the process's local
time zone. On this CEST machine Ruby counted 14 missed publications where Go counted 11. Under `TZ=UTC`, which
production uses, they agree.

## Deliberate deviations from Ruby

The step docs have the full detail. The ones that could matter:

**Output**
- Floats print shortest (`1` where Ruby prints `1.0`), JSON key order can differ, and error wording can differ.
- v1 `Roundable` values over 5,000 are floats, where Ruby returns Integers.
- Log lines are slog records with attributes instead of `KEY: message` strings.

**v1 API**
- Database errors return 500, where Ruby's catch-all returns 422.
- A date passed through `captures[]` must be YYYY-MM-DD.
- Form bodies don't feed parameters.
- `upcase` uses simple case mapping (`ß` stays as is).
- Static files have no Last-Modified header.
- A body first written after the deadline becomes a 500 JSON response.

**v2 API**
- Streaming buffers 4 KB before writing through, and a failure mid-stream aborts the connection, as Puma does.
- Unknown parameters are listed sorted.
- `blend-parity` draws its random shapes with PCG instead of Ruby's MT, so the same seed gives different samples. The
  adversarial shapes are the same.

**Scheduling**
- `publishes_missed` always uses UTC.
- The scheduler's cron runs in the local zone, as rufus does. Production is UTC, and so is the distroless image.
- `frankfurter start` runs web and scheduler in one process instead of foreman's two.

**Database**
- Each migration runs in its own transaction.
- Migrations 027 and 028 clear `blended_rates` instead of recomputing it, but only when they relabelled something. The
  scheduler's existing rebuild refills it. Production is already past 040.
- `VERSION` must be an integer.
- A backfill takes `BEGIN IMMEDIATE` up front instead of upgrading a deferred transaction.

**Tasks and cache**
- `rollups-rebuild` purges the CDN even when its source transaction fails.
- Empty Cloudflare credentials count as unconfigured.

**Numerics**
- Ruby's `%.12g` treats about 2 in 20,000 17-digit near-ties as ties and Go doesn't. The difference is 1e-12 relative.

**Extraction**
- PDFium reads a non-breaking space as a plain space, and its glyph widths can differ by a fraction of a point. It
  also decodes old Japanese fonts that pdf-reader garbles.
- The in-house .xls reader returns formula cells as empty and assumes the 1900 date system.
- No adapter output is affected by either.

## Dependencies

| Module | Version | Licence | Use |
|---|---|---|---|
| modernc.org/sqlite | v1.60.0 | BSD-3-Clause | SQLite, pure Go |
| github.com/klippa-app/go-pdfium | v1.21.0 | MIT | PDF text via PDFium compiled to WebAssembly (wazero, Apache-2.0) |
| github.com/xuri/excelize/v2 | v2.11.0 | BSD-3-Clause | XLSX |
| github.com/richardlehane/mscfb | v1.0.7 | Apache-2.0 | Compound File container under the in-house BIFF8 reader |
| github.com/PuerkitoBio/goquery | v1.13.0 | BSD-3-Clause | HTML |
| github.com/adhocore/gronx | v1.20.5 | MIT | cron next and previous fire times |
| golang.org/x/net | v0.59.0 | BSD-3-Clause | charset detection for HTML and XML |
| golang.org/x/text | v0.42.0 | BSD-3-Clause | legacy encodings |
| github.com/getkin/kin-openapi | v0.149.0 | MIT | OpenAPI response validation (tests only) |
| gopkg.in/dnaeon/go-vcr.v4 | v4.0.7 | BSD-2-Clause | cassette replay (tests only) |
| go.yaml.in/yaml/v3 | v3.0.5 | Apache-2.0 | cassette converter |

The binary links only BSD, MIT and Apache-2.0 modules.

Two dependencies were replaced mid-run:

- shakinm/xlsReader (GPL-3.0, dormant) became `internal/xls`, a 360-line BIFF8 reader on mscfb.
- The converter moved off yaml/v4. `go.yaml.in/yaml/v4 v4.0.0-rc.6` is still an indirect requirement because go-vcr
  pulls it in, but it is test-only and not linked into the binary.

## Known gaps and risks

1. **Cutover isn't wired.** `Procfile`, the root `Dockerfile` and the deploy script on frank still call
   `bundle exec rake`, `bin/schedule` and Puma.
   - The only change outside `go/` is a `test-go` job in `.github/workflows/ci.yml` (5f35c922). It runs gofmt, vet
     and tests, despite the rule against editing outside `go/`.
2. **The image has never been built.** Docker isn't installed here. Its build line cross-compiles cleanly, but the
   distroless runtime, the uid 1000 volume and the healthcheck haven't been exercised.
3. **No live fetches.** Every adapter is verified against recorded cassettes only. Live sources could still expose:
   - differences in query encoding (`url.Values` escapes `$` and spaces where http.rb doesn't);
   - how redirects are handled;
   - TLS behaviour. `adapter.NewClient` sets `InsecureSkipVerify` and redoes full chain verification itself, to trust
     the embedded intermediates. That is correct as written but security-sensitive, so it deserves a human review.
4. **Performance is unmeasured.** There are no benchmarks and no load test. Production needs a 240 s timeout for the
   cold 87 MB export under Ruby. How modernc's pure-Go SQLite and PDFium on wazero behave under that load and memory
   pressure is unknown.
5. **Coverage is narrow in places.**
   - `frankfurter start` as a whole has no test; its two halves do.
   - The smoke corpus is 99 requests on one snapshot.
   - The api_v2 step also ran about 16,000 generated requests through both apps with no mismatches, but on fixture
     data, and its generator isn't committed.
6. **Double maintenance.** While both apps live:
   - `internal/seeds/data` copies `db/seeds` and `internal/api/public` copies `lib/public`. Tests catch drift, but
     every edit needs `go generate`.
   - Money gem upgrades need `money.json` regenerated.
   - Each new Ruby migration needs a Go twin and a fresh `schema.sql`.
7. **Size.** Go non-test code is about 32.6k lines (13.8k core and tooling, 18.8k adapters) against Ruby's roughly 16.5k
   (lib, migrations, tasks, bin). Part of the difference is Ruby library behaviour reimplemented in Go:
   - pdf-reader's layout;
   - Rack's nested query parser;
   - the spreadsheet gem's typing;
   - the BIFF8 reader;
   - the VCR converter.
8. **Environment finding: the default dev database was overwritten.** In the main checkout, `db/frankfurter.sqlite3`
   (the database used when `APP_ENV` is unset) was rewritten on 2026-09-27 at 13:26 with fixture data: 3 providers,
   8,840 rates, 1.9 GB mostly free pages. `frankfurter_test.sqlite3` was written 14 s later. That matches a spec run
   without `APP_ENV=test`. `frankfurter_development.sqlite3` (2026-09-14, 2.6 GB) looks intact.

## Next steps

1. Review the security-sensitive and central code first: `adapter.NewClient`, `ratequery`, `blend`, `migrate`.
2. Build the image and run it on frank in shadow:
   - a Go scheduler backfilling from live sources into its own database, with a daily row-level diff against Ruby's
     database for a week or two;
   - `frankfurter serve` on a side port, with `smokeparity` replaying sampled production request paths against both.
3. Load-test the heavy paths (cold export, long daily ranges, `expand=providers`) against Puma, and tune `MAX_THREADS`
   and the heavy slots.
4. Switch the Procfile, Dockerfile, deploy script and CI publish job to `frankfurter <command>`, keeping the last Ruby
   image as the rollback.
5. After cutover, stop the double maintenance:
   - make Go the source of seeds, public files and migrations, and retire the Ruby tree;
   - retire the porting tools (`vcrconvert`, `genadapters`, and `golden.rb`, `api_golden.rb` and `blend_golden.rb`
     under `scripts/`) once no Ruby reference remains.
