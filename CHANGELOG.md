# Changelog

All notable changes to the Frankfurter API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- v2 API at `/v2/` endpoints with multi-provider blended exchange rates
- 25 data providers: ECB, BOC, TCMB, NBU, CBA, NBRB, BOB, CBR, NBP, NBP.B, FRED, BNM, RBA, BCRA, CBK, BOJ, IMF, NBRM, BCEAO, BOI, BCCR, NB, NBG, HKMA, RB
- 150+ currencies (up from ~30 in v1)
- `/v2/rate/{base}/{quote}` endpoint for single currency pair lookups
- `/v2/rate/{base}/{quote}/{date}` for historical single pair lookups
- `/v2/providers` endpoint listing available data sources with date ranges and currency coverage
- `/v2/currencies` endpoint with provider coverage per currency
- `providers` parameter to scope rates to specific sources
- `group` parameter to downsample time series (`week` or `month`)
- Strict parameter validation — unknown parameters return 422

### Fixed

- Fixed rounding when amount is 1 and base is EUR (#173)

### Removed

- Removed JSONP

### Migrating from v1

The v1 API will remain available at `/v1/` endpoints. To migrate to `/v2`:

- Change your base URL from `/v1/latest` to `/v2/rates`.
- Update response parsing: rates are now an array of `{"date", "base", "quote", "rate"}` objects instead of `{"base", "date", "rates": {"USD": 1.23}}`.
- The `symbols` parameter is renamed to `quotes`.
- The `from` and `to` parameters are now used for date ranges in v2.

## [1.0.0] - 2024-12-04

### Changed

- API versioning in URL path (v1)
- Migrated from PostgreSQL to SQLite
- Moved domain from <https://api.frankfurter.app> to <https://api.frankfurter.dev>. Former will continue serving the old
  unversioned paths.

[Unreleased]: https://github.com/lineofflight/frankfurter/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/lineofflight/frankfurter/releases/tag/v1.0.0
