# Changelog

All notable changes to the Frankfurter API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Banco Nacional de Angola (BNA) as a data provider. Daily reference rates for ~70 currencies against AOA from 2000-01-01.
- Central Bank of Iraq (CBI) as a new data provider. Publishes daily Iraqi dinar (IQD) reference rates against ~16 currencies, 2009-present.
- Maldives Monetary Authority (MMA) as a provider for USD/MVR reference rates, with daily coverage from 2011-04-21.

### Fixed

- `/v2/rates` date-range responses are now sorted by date. Rows where carry-forward surfaced an older quote previously appeared out of order.

## [2.0.2] - 2026-05-21

### Fixed

- Ensure `/v2/rates` date-range queries return the same rates as `/v2/rates?date=…` for any given date.

## [2.0.1] - 2026-05-19

### Fixed

- `/v2/rates` date-range queries no longer duplicate the first row of the response.

## [2.0.0] - 2026-05-18

New multi-provider API at `/v2/`. The v1 API is unchanged and remains available indefinitely at `/v1/`.

### Migrating from v1

- Change your base URL from `/v1/latest` to `/v2/rates`.
- Rates are now an array of `{"date", "base", "quote", "rate"}` objects instead of `{"base", "date", "rates": {"USD": 1.23}}`.
- The `symbols` parameter is renamed to `quotes`.
- `from` and `to` are used for date ranges.
- JSONP is not supported in v2.

### Added

- `/v2/rates` — blended exchange rates from 50+ institutional providers, derived from a USD-anchored blend so reciprocals and cross-rate triangles round to 1.0. Non-USD-base queries may differ from a single provider in the 4th to 5th decimal. (#343)
- `/v2/rate/{base}/{quote}` and `/v2/rate/{base}/{quote}/{date}` — single pair, latest or historical.
- `/v2/providers` — data sources with date ranges, currency coverage, `rate_type`, and `country_code`.
- `/v2/currencies` — provider coverage per currency, including peg metadata (anchor and fixed rate).
- Precious metal quotes (XAU, XAG, XPT, XPD).
- IMF Special Drawing Rights (XDR), including SDR cross rates as a primary source. (#333, #335)
- Historical currency support — pre-euro and pre-redenomination codes (DEM, FRF, NLG, CYP, etc.) where providers serve them.
- Pegged currency expansion at query time; pegged rates snap to the exact peg, and cross-base requests for pegged quotes are anchored through the peg's base. Pegs act as a source, so `?providers=` excludes them along with other unlisted sources. (#323)
- `expand=providers` on `/v2/rates` — each provider's individual rate as `[{ "key", "rate" }]`; excluded providers (outliers, peg overrides) are flagged `excluded: true`. CSV form `ECB:0.92|BOC:0.93`, `*`-suffixed when excluded. (#323)
- `providers` parameter to scope rates and currencies to specific sources.
- `group` parameter to downsample time series (`week` or `month`).
- CSV and NDJSON streaming output.
- Outlier detection and recency-weighted blending.
- Rows are stamped with their actual observation date; range queries do not carry forward. (#338)
- Strict parameter validation — unknown parameters return 422.
- Error responses are not cacheable, including streaming range queries.

_Pre-release history: see the [v2.0.0-beta.1](https://github.com/lineofflight/frankfurter/releases/tag/v2.0.0-beta.1) and [v2.0.0-beta.2](https://github.com/lineofflight/frankfurter/releases/tag/v2.0.0-beta.2) release notes._

## [1.0.0] - 2024-12-04

### Changed

- API versioning in URL path (v1)
- Migrated from PostgreSQL to SQLite
- Moved domain from <https://api.frankfurter.app> to <https://api.frankfurter.dev>. Former will continue serving the old
  unversioned paths.

[2.0.2]: https://github.com/lineofflight/frankfurter/compare/v2.0.1...v2.0.2
[2.0.1]: https://github.com/lineofflight/frankfurter/compare/v2.0.0...v2.0.1
[2.0.0]: https://github.com/lineofflight/frankfurter/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/lineofflight/frankfurter/releases/tag/v1.0.0
