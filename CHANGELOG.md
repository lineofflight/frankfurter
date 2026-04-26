# Changelog

All notable changes to the Frankfurter API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Deutsche Bundesbank (BBK) as historical provider — daily pre-euro Frankfurt fixings for 18 currencies, 1948-06-21 through 1998-12-30

### Fixed

- Restored Bank Negara Malaysia (BNM) provider that was inadvertently removed when the National Bank of Moldova key was renamed to BNM. Moldova's key is now NBM again.
- BNR (National Bank of Romania) XAU rate is now normalized to RON per troy ounce. BNR publishes XAU as RON per gram, but ISO 4217 defines XAU as one troy ounce — the mistakenly low values were skewing blended XAU rates by ~31×. (#323)
- BNR backfill now reads the yearly XML archive for the current year instead of the 10-day rolling feed. The 10-day feed left a permanent gap whenever a fresh backfill or scheduler outage spanned more than 10 days; the yearly archive covers all published cubes for the year and is a strict superset of the 10-day window.

## [2.0.0-beta.2] - 2026-04-14

### Added

- 15 new providers: BCN, BCU, BDI, BI, BNR, BOT, CBM, CBU, CNB, DNB, HNB, LB, MAS, MNB, NBK, NRB, SARB — bringing the total to 52
- Historical currency support — pre-euro and pre-redenomination codes (DEM, FRF, NLG, CYP, etc.) now available where providers serve them
- Peg metadata on `/v2/currencies` — pegged currencies show their anchor and fixed rate
- `rate_type` and `country_code` fields on `/v2/providers`

### Changed

- Renamed Bank of Tanzania provider key from BOT to BOTA
- Replaced `description` with `rate_type` and `country_code` on `/v2/providers`
- Pegged currency rates now snap to their exact peg rate
- Extended historical coverage for several providers

### Fixed

- CDN cache now purges after importing new rates
- V2 rates now sorted by quote for deterministic output (#304)
- Various adapter parsing and rate direction fixes (HNB, ECB, NRB, CBA)

## [2.0.0-beta.1] - 2026-04-02

### Added

- v2 API at `/v2/` endpoints with multi-provider blended exchange rates
- 35 data providers: ECB, BOC, TCMB, NBU, CBA, NBRB, BOB, CBR, NBP, NBP.B, FRED, BNM, RBA, BCRA, CBK, BOJ, BOJA, IMF, NBRM, BCEAO, BOI, BCCR, NB, NBG, HKMA, RB, BCB, BCCh, Banxico, BOE, FBIL, BANREP, SBI, CBC, BAM, BOT, Riksbank
- Precious metal quotes (XAU, XAG, XPT, XPD)
- Pegged currency expansion at query time
- `/v2/rate/{base}/{quote}` endpoint for single currency pair lookups
- `/v2/rate/{base}/{quote}/{date}` for historical single pair lookups
- `/v2/providers` endpoint listing available data sources with date ranges and currency coverage
- `/v2/currencies` endpoint with provider coverage per currency
- `providers` parameter to scope rates and currencies to specific sources
- `group` parameter to downsample time series (`week` or `month`)
- CSV and NDJSON streaming output for rate endpoints
- Outlier detection and filtering for rate quality
- Recency-weighted blending
- Strict parameter validation — unknown parameters return 422

### Fixed

- Fixed rounding when amount is 1 and base is EUR (#173)
- Fixed TCMB rate direction by switching to buy/sell series
- Fixed CBK inverted cross rates and per-unit parsing
- Fixed NBRM rate parsing for denominated currencies
- Fixed BCRA dates with duplicate currency entries
- Fixed BCEAO number format parsing

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

[Unreleased]: https://github.com/lineofflight/frankfurter/compare/v2.0.0-beta.2...HEAD
[2.0.0-beta.2]: https://github.com/lineofflight/frankfurter/compare/v2.0.0-beta.1...v2.0.0-beta.2
[2.0.0-beta.1]: https://github.com/lineofflight/frankfurter/compare/v1.0.0...v2.0.0-beta.1
[1.0.0]: https://github.com/lineofflight/frankfurter/releases/tag/v1.0.0
