# Changelog

All notable changes to the Frankfurter API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Central Bank of Turkmenistan (CBT) as a data provider. Daily official rates for ~45 currencies against the Turkmenistani manat (TMT) from 2020-04-17. USD/TMT has been pegged at 3.5 since 2015.
- Banco Nacional de Angola (BNA) as a data provider. Daily reference rates for ~70 currencies against AOA from 2000-01-01.
- Central Bank of Iraq (CBI) as a new data provider. Publishes daily Iraqi dinar (IQD) reference rates against ~16 currencies, 2009-present.
- Bank of Algeria (BoA) as a data provider. Publishes a daily DZD reference rate (cours moyen) against 17 currencies, with history back to 2000-01-03. The consolidated archive refreshes roughly monthly, so rates can lag by up to ~3 weeks.
- Central Bank of Egypt (CBE) provider: daily EGP rates against 18 currencies from 2024-03-01 onward, with buy/sell coerced to mid. (#366)
- Maldives Monetary Authority (MMA) as a provider for USD/MVR reference rates, with daily coverage from 2011-04-21.
- Banco Central del Paraguay (BCP) as a data provider. Weighted-average interbank reference rates against the Paraguayan guaraní (PYG); USD/EUR back to 2001, most others from around 2012.
- State Bank of Pakistan (SBP) as a data provider. Daily Average Banks' Floating Exchange Rates against the Pakistani rupee (PKR), 2013-07-02 to present. Published as monthly snapshots that trail by up to ~3 weeks.
- Central Bank of Sri Lanka (CBSL) as a data provider. Daily indicative rates for 55 currencies (including XAU per troy ounce) against the Sri Lankan rupee (LKR), from 2011-01-20.
- National Bank of Cambodia (NBC) as a data provider. Daily reference rates for ~29 currencies against the Cambodian riel (KHR), from 2008-01-14.
- Banque Centrale de Tunisie (BCT) as a data provider. Daily interbank reference rates for 20 currencies against the Tunisian dinar (TND), from 2004-12-23.
- National Bank of Tajikistan (NBT) as a data provider. Daily rates for ~36 currencies against the Tajikistani somoni (TJS), back to 2001-01-01.
- National Bank of the Kyrgyz Republic (NBKR) as a data provider. Daily rates for 5 majors (USD, EUR, RUB, KZT, CNY) and weekly rates for ~35 other currencies against the Kyrgyz som (KGS), with carry-forward filling gaps between weekly publications. Forward-only from 2026-05-15.
- Bank of Mongolia (BOM) as a data provider. Daily statutory reference rates for 39 currencies plus XAU and XAG against MNT, back to 2001-01-02.
- Central Bank of Nigeria (CBN) as a data provider. Daily official rates against the Nigerian naira (NGN) using the mid of buy/sell, from 2001-12-10.
- National Bank of Ethiopia (NBE) as a data provider. Daily weighted-average rates for 18 currencies against the Ethiopian birr (ETB) from 2024-10-01, skipping the July to September 2024 float-transition gap.
- National Reserve Bank of Tonga (NRBT) as a data provider. Daily MID rates for 12 currencies (AUD, CAD, CHF, EUR, FJD, GBP, JPY, NZD, SEK, SGD, USD, WST) against the Tongan paʻanga (TOP), from 2017-01-03.

### Changed

- XDR (Special Drawing Rights, ISO 4217) is now passed through consistently. BOM, NBC, and CBN rewrite their non-ISO "SDR" label to XDR; NBE no longer drops XDR. CBI and IMF already emitted XDR. BCP exposes an SDR endpoint but its page returns 100% ND, so BCP stays excluded.

### Fixed

- `/v2/rates` date-range responses are now sorted by date. Rows where carry-forward surfaced an older quote previously appeared out of order.
- `/v2/rates` no longer returns 500 errors for date/provider/quote combinations where an upstream provider published a zero rate (e.g. BNM's THB/MYR on certain 2006-2018 dates). Non-positive rates are now dropped on ingest.

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
