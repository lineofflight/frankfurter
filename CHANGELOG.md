# Changelog

All notable changes to Frankfurter will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- v2 API with multi-provider blended exchange rates
- Normalized response format (one record per currency pair)
- `GET /v2/rates` with date, base, symbols, and provider filtering
- `GET /v2/currencies` and `GET /v2/providers` endpoints
- Bank of Canada (BOC) as second data provider (CAD base, data since 2017)
- Provider architecture (`Providers::Base`) with scope/import pattern
- OpenAPI 3.1 spec at `/v2/openapi.json` with schema validation in tests
- Separate SQLite databases per environment (`APP_ENV`)
- Healthcheck in Dockerfile

### Changed

- Renamed `currencies` table to `rates`, `source` column to `provider`
- Renamed `Currency` model to `Rate`
- Moved v1 legacy code into `Versions::V1` namespace
- Per-provider rake tasks (`ecb:import`, `boc:import`, `ecb:backfill`, `boc:backfill`)
- Return latest rates for future dates

### Fixed

- Do not round exchange rates when amount is 1 and base is EUR (fixes #173)

### Removed

- Removed `Bank` module and `Bank::Feed` (replaced by providers)
- Removed JSONP

## [1.0.0] - 2024-12-04

### Changed

- API versioning in URL path (v1)
- Migrated from PostgreSQL to SQLite
- Moved domain from <https://api.frankfurter.app> to <https://api.frankfurter.dev>. Former will continue serving the old
  unversioned paths.

[Unreleased]: https://github.com/lineofflight/frankfurter/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/lineofflight/frankfurter/releases/tag/v1.0.0
