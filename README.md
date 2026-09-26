# Frankfurter

[Frankfurter](https://frankfurter.dev) is an open-source currency data API that tracks daily exchange rates from central banks and official sources.

## History coverage

`GET /v2/coverage` describes the default EUR-based daily blended feed. Pass the same `base`, `quotes`, and `providers`
filters as a rates query to get its coverage, for example `/v2/coverage?base=USD&quotes=ZAR&providers=BIS`.
Non-daily provider history remains available through explicit provider queries and does not enter the daily blend.

The existing `/v2/currencies?scope=all` catalogue covers eligible daily sources plus pegged currencies, while
`/v2/currencies?providers=BIS` describes that provider's recognized currencies. Those currency dates, and the dates
in `/v2/providers`, describe publication coverage. They do not establish that a requested base and quote can be
converted at the same time. Taking their minimum can advertise history that the default rates query cannot return.

For a website headline describing the default API, use **`start_date` from `/v2/coverage`**:

```javascript
const coverage = await fetch("https://api.frankfurter.dev/v2/coverage").then(r => r.json());
const headlineYear = coverage.start_date?.slice(0, 4); // Hide the history claim when null.
```

Use matching filters for pair or provider pages. Both bounds are eligible observation dates at which the selected
rates query returns at least one record; they can contain gaps. A carried quote's returned date may precede the
first date its base bridge exists. The bounds do not extend history into future days merely because carry-forward
can answer them. Multiple quotes use the rates endpoint's semantics: any matching pair suffices. No queryable pair
returns null bounds. Coverage accepts only `base`, `quotes`, and `providers`.

## Deployment

The public API runs at <https://api.frankfurter.dev>. If you prefer to host your own instance, you can run Frankfurter with Docker.

### Using Docker

The quickest way to get started:

```bash
docker run -d --init -p 8080:8080 lineofflight/frankfurter
```

For production, mount a volume to persist the SQLite database across container restarts and set any optional API keys:

```bash
docker run -d --init -p 8080:8080 \
  -e DATABASE_URL=sqlite:///app/data/frankfurter.sqlite3 \
  -e BAM_API_KEY=your_key \
  -e BANXICO_API_KEY=your_key \
  -e BCCH_USER=your_email \
  -e BCCH_PASS=your_password \
  -e BOT_API_KEY=your_key \
  -e FRED_API_KEY=your_key \
  -e TCMB_API_KEY=your_key \
  -v ./data:/app/data \
  --pull always \
  lineofflight/frankfurter
```

Without a mounted volume, the database is ephemeral and some endpoints may return empty data until their initial backfill completes.

Some data providers require API keys. All are free and optional:

- `BAM_API_KEY` — Bank Al-Maghrib (Morocco). Register at [apihelpdesk.centralbankofmorocco.ma](https://apihelpdesk.centralbankofmorocco.ma/apis).
- `BANXICO_API_KEY` — Banco de México. Register at [banxico.org.mx](https://www.banxico.org.mx/SieAPIRest/service/v1/).
- `BCCH_USER` / `BCCH_PASS` — Banco Central de Chile. Register at [si3.bcentral.cl](https://si3.bcentral.cl/Siete/es/Siete/API).
- `BOT_API_KEY` — Bank of Thailand. Register at [portal.api.bot.or.th](https://portal.api.bot.or.th).
- `FRED_API_KEY` — Federal Reserve. Register at [fred.stlouisfed.org](https://fred.stlouisfed.org/docs/api/api_key.html).
- `TCMB_API_KEY` — Turkish Central Bank. Register at [evds3.tcmb.gov.tr](https://evds3.tcmb.gov.tr).

## Contributing

### Restoring provider history

Backfills are incremental by default. To re-fetch previously omitted quotes from each provider's `coverage_start`:

```bash
FULL=1 bundle exec rake backfill          # All providers
FULL=1 bundle exec rake 'backfill[CBKKW]' # One provider
```

This inserts missing rows without replacing existing quotes. Per-day APIs can take days to finish. After the backfills
finish, run `bundle exec rake blend:rebuild` to rebuild daily, weekly and monthly blends, then
`bundle exec rake blend:parity` to check materialized results against live computation.

Provider routes retain retired and unknown currency codes. Blends and the currency catalogue exclude unknown codes
and observations on or after a currency's terminal date. Early successor labels are relabelled to their configured
predecessor. Provider health reports unknown codes for registry updates.

See [AGENTS.md](AGENTS.md) for development setup and guidelines.

Built a library or tool with Frankfurter? Share it in [Show and Tell](https://github.com/lineofflight/frankfurter/discussions/categories/show-and-tell)
