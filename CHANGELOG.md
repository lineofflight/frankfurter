# Changelog

All notable changes to the Frankfurter API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Per-provider routes: `/v2/{provider}/rates` and `/v2/{provider}/rate/{base}/{quote}` serve one provider's rates as published, identical to the `providers=` filter. (#643)
- Nicaraguan córdoba (NIO) peg at 36.6243 per US dollar. (#604)

### Changed

- Daily ranges filtered to a single provider are no longer capped at 5 years. (#644)
- Daily ranges computed live (`providers=`, `expand=providers`, or before the precomputed blend is ready) are limited to a few at a time; excess requests get an immediate 503 with a `Retry-After` header instead of queueing. (#650)

### Removed

- Banco Central de Nicaragua (BCN) as a data provider; NIO is now served from its peg. (#604)

### Fixed

- Peg rates no longer override provider rates for dates before the peg took effect. (#603)

## [2.4.0] - 2026-09-09

### Added

- Bank of Zambia (BOZ) as a data provider. (#389)
- Central Bank of Kuwait (CBKKW) as a data provider. (#447)
- Banque du Liban (BdL) as a data provider. (#381)
- Central Bank of Seychelles (CBSSC) as a data provider. (#404)
- Central Bank of Oman (CBO) as a data provider. (#448)
- Banco Central de Venezuela (BCV) as a data provider. (#435)
- Central Bank of the Republic of Azerbaijan (CBAR) as a data provider. (#606)
- Central Bank of Trinidad and Tobago (CBTT) as a data provider. (#378)
- Centrale Bank van Suriname (CBvS) as a data provider. (#426)
- China Foreign Exchange Trade System (CFETS) as a data provider. (#605)
- Palestine Monetary Authority (PMA) as a data provider. (#454)
- Banque Centrale du Congo (BCCCD) as a data provider. (#398)
- Banco de Moçambique (BM) as a data provider. (#386)
- Banco Central de Reserva del Perú (BCRP) as a data provider. (#607)
- Deprecation and Link headers (RFC 9745) on V1 responses pointing to V2.
- Observation dates in V2 `expand=providers` payloads.
- Base currency identity records (`USD/USD = 1`) in V2 rate responses. (#538)

### Changed

- Full-history date ranges on V2 rates are now served in seconds from a precomputed blend, lifting the 5-year cap. (#569, #570)
- V2 latest rates now include provider observations dated one day ahead of the service date for next-day publishers.

### Fixed

- Canonical rate consistency across single-date, latest, and range query shapes. (#570, #573)
- V2 range queries return 503 on timeout instead of computing indefinitely. (#569)
- Relabelled historical predecessor currencies (AZM, TMM, ZMK, RUR, BRR, TJR) previously reported under current codes. (#621, #623, #624)
- Restored stalled provider feeds for BCBO, BCT, BNR, CBE, and SBP. (#547, #548, #564, #576, #580)
- Ingest precision and rounding fixes for CBK, TCMB, and BI feeds. (#579, #584, #585)
- Fixed Docker healthcheck failure under load. (#556)
- Date-relative V2 responses expire from CDN caches at UTC midnight instead of after 24 hours. (#541)

## [2.3.5] - 2026-06-25

### Changed

- Bangko Sentral ng Pilipinas (BSP) now contributes only its official USD/PHP reference rate. (#533)

## [2.3.4] - 2026-06-25

### Fixed

- Single-provider V2 queries now preserve the source's native precision instead of rounding to magnitude bands. (#534)

## [2.3.3] - 2026-06-22

### Fixed

- Restored Reserve Bank of Vanuatu (RBV) rates after bundling missing intermediate TLS certificates.
- Restored Central Bank of Samoa (CBS) rates after handling unescaped spaces in source filenames.
- Labelled pre-1999 Sveriges Riksbank euro rates as ECU (XEU) instead of EUR.
- Prevented ingestion of euro-denominated rates dated before euro inception (1999-01-04).
- Retired legacy currencies (ATS, BEF, DEM, ESP, FRF, ITL, NLG, PTE) at their respective euro changeover dates.
- Weekly and monthly time series no longer omit the current, in-progress period. (#521)

## [2.3.2] - 2026-06-13

### Fixed

- Prevented ingestion of stray future-dated rates that froze incremental provider updates.

## [2.3.1] - 2026-06-11

### Fixed

- Docker containers no longer terminate on startup when the scheduler staggers provider backfills. (#514)

## [2.3.0] - 2026-06-11

### Added

- Banco Central de Bolivia (BCBO) multi-currency basket and commodity reference prices (XAU, XAG, XDR).
- Multi-architecture Docker image builds (`linux/amd64` and `linux/arm64`). (#140)

### Fixed

- Sped up long `/v2/rates` time-series exports with a sliding calculation window.
- Added `stale-while-revalidate` and `stale-if-error` caching directives to time-series exports.
- Fixed 500 errors on date ranges crossing Lithuania's 2015 euro changeover.
- Restored Central Bank of Samoa (CBS) rates following daily filename updates.

## [2.2.0] - 2026-06-01

### Added

- Bangko Sentral ng Pilipinas (BSP) as a data provider.
- Banco Central de Bolivia (BCBO) as a data provider.
- Added `publish_cadence` metadata to `/v2/providers`.

### Fixed

- Set CBC publish cadence to monthly to avoid inaccurate missed publish counts.
- Restored Bank of Tanzania (BOTA) rates after handling CSRF token validation.

## [2.1.1] - 2026-05-29

### Fixed

- Restored alphabetical quote sorting for latest `/v2/rates` responses with carried-forward rates.

## [2.1.0] - 2026-05-24

### Added

- Banco Central de Cuba (BCC) as a data provider.
- Banque Nationale du Rwanda (BNRRW) as a data provider.
- Banco Nacional de Angola (BNA) as a data provider.
- Central Bank of Iraq (CBI) as a data provider.
- Bank of Algeria (BoA) as a data provider.
- Central Bank of Egypt (CBE) as a data provider. (#366)
- Maldives Monetary Authority (MMA) as a data provider.
- Banco Central del Paraguay (BCP) as a data provider.
- State Bank of Pakistan (SBP) as a data provider.
- Central Bank of Sri Lanka (CBSL) as a data provider.
- National Bank of Cambodia (NBC) as a data provider.
- Banque Centrale de Tunisie (BCT) as a data provider.
- National Bank of Tajikistan (NBT) as a data provider.
- National Bank of the Kyrgyz Republic (NBKR) as a data provider.
- Bank of Mongolia (BOM) as a data provider.
- Central Bank of Nigeria (CBN) as a data provider.
- National Bank of Ethiopia (NBE) as a data provider.
- National Reserve Bank of Tonga (NRBT) as a data provider.
- Central Bank of Samoa (CBS) as a data provider.
- Autoridade Monetária de Macau (AMCM) as a data provider.
- Central Bank of Liberia (CBLLR) as a data provider.
- Reserve Bank of Fiji (RBF) as a data provider.
- Da Afghanistan Bank (DAB) as a data provider.
- Central Bank of The Gambia (CBG) as a data provider.
- Reserve Bank of Malawi (RBM) as a data provider.
- Reserve Bank of Vanuatu (RBV) as a data provider.
- Banque de la République du Burundi (BRB) as a data provider.
- Support for Turkmenistani manat (TMT) peg to USD.

### Changed

- Standardized Special Drawing Rights under the ISO 4217 code `XDR`.

### Fixed

- Sorted `/v2/rates` date-range responses chronologically.
- Dropped non-positive rates on ingest to avoid 500 errors.
- Dropped defunct ISO 4217 currency codes past their retirement dates on ingest.

## [2.0.2] - 2026-05-21

### Fixed

- Ensure `/v2/rates` date-range queries return the same rates as `/v2/rates?date=…` for any given date.

## [2.0.1] - 2026-05-19

### Fixed

- Prevented duplicate first row in `/v2/rates` date-range queries.

## [2.0.0] - 2026-05-18

New multi-provider API at `/v2/`. The v1 API is unchanged and remains available indefinitely at `/v1/`.

### Migrating from v1

- Change your base URL from `/v1/latest` to `/v2/rates`.
- Rates are now an array of `{"date", "base", "quote", "rate"}` objects instead of `{"base", "date", "rates": {"USD": 1.23}}`.
- The `symbols` parameter is renamed to `quotes`.
- `from` and `to` are used for date ranges.
- JSONP is not supported in v2.

### Added

- Blended exchange rates from 50+ institutional providers at `/v2/rates`. (#343)
- Single pair endpoints at `/v2/rate/{base}/{quote}` and `/v2/rate/{base}/{quote}/{date}`.
- Provider and currency metadata endpoints at `/v2/providers` and `/v2/currencies`.
- Support for precious metals (XAU, XAG, XPT, XPD) and IMF Special Drawing Rights (XDR). (#333, #335)
- Historical currency coverage for pre-euro and pre-redenomination codes.
- Query-time pegged currency resolution with exact peg anchors. (#323)
- Individual provider breakdowns via `expand=providers`. (#323)
- Provider filtering with `providers` query parameter.
- Time-series downsampling via `group` (`week` or `month`).
- CSV and NDJSON streaming responses.
- Outlier detection and recency-weighted rate blending.
- Stamped observation dates on rates without range carry-forward. (#338)
- Strict query parameter validation (422 on unknown parameters).

## [1.0.0] - 2024-12-04

### Changed

- Added API versioning to URL path (`/v1/`).
- Migrated database storage from PostgreSQL to SQLite.
- Moved domain to <https://api.frankfurter.dev>.

[Unreleased]: https://github.com/lineofflight/frankfurter/compare/v2.4.0...HEAD
[2.4.0]: https://github.com/lineofflight/frankfurter/compare/v2.3.5...v2.4.0
[2.3.5]: https://github.com/lineofflight/frankfurter/compare/v2.3.4...v2.3.5
[2.3.4]: https://github.com/lineofflight/frankfurter/compare/v2.3.3...v2.3.4
[2.3.3]: https://github.com/lineofflight/frankfurter/compare/v2.3.2...v2.3.3
[2.3.2]: https://github.com/lineofflight/frankfurter/compare/v2.3.1...v2.3.2
[2.3.1]: https://github.com/lineofflight/frankfurter/compare/v2.3.0...v2.3.1
[2.3.0]: https://github.com/lineofflight/frankfurter/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/lineofflight/frankfurter/compare/v2.1.1...v2.2.0
[2.1.1]: https://github.com/lineofflight/frankfurter/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/lineofflight/frankfurter/compare/v2.0.2...v2.1.0
[2.0.2]: https://github.com/lineofflight/frankfurter/compare/v2.0.1...v2.0.2
[2.0.1]: https://github.com/lineofflight/frankfurter/compare/v2.0.0...v2.0.1
[2.0.0]: https://github.com/lineofflight/frankfurter/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/lineofflight/frankfurter/releases/tag/v1.0.0
