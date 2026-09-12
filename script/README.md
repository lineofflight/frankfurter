# Rate component experiment

The prototype stores `mid`, `bid` and `ask`, with a virtual `rate` column using
the application's decimal midpoint function. Standalone SQLite clients need
that function registered before selecting `rate`. Existing rollups and blends
keep their materialized rates.

Run the recorded-response comparison after setting up the test database:

```sh
APP_ENV=test bundle exec rake db:setup
APP_ENV=test bundle exec ruby script/rate_components_parity.rb
```

The default run replays VCR cassettes without network access. To compare live
historical responses from the providers used in this experiment:

```sh
APP_ENV=test LIVE_PROVIDERS=CBI,BCRP,BOZ bundle exec ruby \
  script/rate_components_parity.rb tmp/rate-parity/live-components.jsonl
```

Obtain a consistent production snapshot with SQLite's backup API and keep an
untouched baseline. Migrate a separate local copy, then compare/enrich it:

```sh
DATABASE_URL=sqlite:///absolute/path/candidate.sqlite3 bundle exec rake db:migrate
bundle exec ruby script/compare_rate_components.rb \
  /absolute/path/baseline.sqlite3 /absolute/path/candidate.sqlite3 \
  tmp/rate-parity/components.jsonl tmp/rate-parity/live-components.jsonl
```

The comparison script writes only the candidate, leaving source mismatches
and missing observations untouched. It compares every effective raw rate and
the existing materialized tables against the baseline. It does not rebuild
the entire blend or recover discarded components for all providers.

Run `script/rate_components_requests.rb` once per snapshot using
`DATABASE_URL`. Compare its JSONL status/body digests and median request times.

For historical enrichment through the application, use
`rake backfill:components[PROVIDER]` with an explicit local `DATABASE_URL`.
The task starts at the provider's coverage start and updates existing rows
only when their effective value remains identical. It reports missing rows
and source mismatches; an unexpected calculated value rolls back the batch.
