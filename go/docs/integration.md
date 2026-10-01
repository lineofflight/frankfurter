# Integration

What joined the steps together, and the live-data check against the Ruby app.

## Wiring

- `internal/adapters/all/all.go` is regenerated (`go generate ./internal/adapters/all`) and imports all 105 adapter
  packages. BANGUAT is compiled but registers nothing: Ruby keeps the class without a seed (disabled in production).
  `cmd/frankfurter` imports `all`, so serve, schedule, backfill and `rates.ProviderLeads` see every provider.
- `internal/adapters/all/seeded_test.go` ports provider_spec's "resolves all seeded providers": every seeded provider
  resolves through `adapter.Lookup`.
- `frankfurter blend-parity` (`blend:parity`) closes the last binary gap (see core-ops.md).
- `Server.rawPaths` routes paths ServeMux would redirect (`/v1//latest`, `/v2/rate//USD`) the way Roda does, closing
  the deviation api_v1 and api_v2 documented.

## Smoke parity

```sh
go/scripts/smoke_parity.sh [snapshot.sqlite3]   # set GO=/path/to/go if go isn't on PATH
```

The script copies the snapshot (default `db/frankfurter.sqlite3`, only ever read), brings the copy to the latest
migration and seeds it with Ruby's `rake db:setup`, starts Puma (web only, single mode) and `frankfurter serve` on it
(ports `RUBY_PORT`/`GO_PORT`, default 9301/9302), sends `scripts/smoke_corpus.txt` to both through
`scripts/smokeparity`, then stops both and deletes the copy. The comparer checks status, media type,
Content-Disposition and body (JSON by value with numbers within 1e-6 relative and error wording ignored, NDJSON by
line, CSV by field) and validates each Go v2 answer the OpenAPI document describes with kin-openapi.

Both servers run with `TZ=UTC`, as production does. Ruby's `Provider#publishes_missed` reads cron schedules in the
process's local zone (fugit on a local `Time`), so on a CEST laptop Ruby counts 14 missed publications where Go and a
UTC Ruby count 11 for providers publishing around midnight UTC (BANREP, BI, BOJ, BSP, CFETS). Go always uses UTC.

Result on the 2026-09-13 daily production backup (12.1M rates, blended tables populated, migrated from 32 to 40): 99
requests, 99 matched, 57 Go v2 answers conform to the document.

`db/frankfurter.sqlite3` in the main checkout was not usable as the snapshot: it was last written on 2026-09-27 and
holds only fixture data (3 providers, 8,840 rates, 1.9 GB of free pages), the signature of a Ruby spec run without
`APP_ENV=test` wiping it. Both apps matched on it too, apart from the two issues above, but it exercises little.

## Backfill fixes

A full local backfill from an empty database turned up these gaps:

- Every window boundary lost a day, and a first backfill skipped `coverage_start` (see core-provider.md). Fixed on both
  sides.
- AMCM stamps rows before mid-2012 with a time of day (`2012-05-14 14:00:00`). Go kept it, which put the row after
  midnight of `upto` and dropped each window's last day. It now keeps the date only, as Ruby's `Date.parse` does.
  Go only.

## Left outside go/

The CI workflow (`.github/workflows/ci.yml`), `Procfile` and the root Dockerfile still call `bundle exec rake`,
`bin/schedule` and Puma, as does the server-side deploy script on frank. Switching them to `frankfurter <command>`
and `go/Dockerfile` is a change to the Ruby tree, which the port does not touch.
