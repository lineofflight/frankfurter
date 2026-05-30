# Changelog

All notable changes to the Frankfurter API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `/v2/providers` now includes `publish_cadence` (`daily`, `weekly`, `monthly`, or `null`) for each provider. It gives the unit for `publishes_missed`, so consumers can tell whether a provider is one day, week, or month behind.

### Fixed

- CBC no longer reports a false "missed publishes" count. Its multi-currency feed is refreshed in monthly batches in arrears, not daily, so it is now treated as a monthly publisher.

## [2.1.1] - 2026-05-29

### Fixed

- Latest `/v2/rates` responses are alphabetical by quote again. A quote carried forward from an earlier date (e.g. a stale or infrequently published currency) was sorted ahead of the rest by its older date, breaking the ordering. Range responses remain ordered by date.

## [2.1.0] - 2026-05-24

### Added

- Banco Central de Cuba (BCC) as a data provider. Daily informal-market rates for 13 currencies against the Cuban peso (CUP), from 2025-12-19.
- Banque Nationale du Rwanda (BNRRW) as a data provider. Daily reference rates for 16 currencies against the Rwandan franc (RWF), back to 2012-01-02.
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
- National Reserve Bank of Tonga (NRBT) as a data provider. Daily reference rates for 12 currencies against the Tongan paʻanga (TOP), from 2017-01-03.
- Central Bank of Samoa (CBS) as a data provider. Daily indicative rates for 9 currencies against the Samoan tala (WST), from 2008-01-03.
- Autoridade Monetária de Macau (AMCM) as a data provider. Daily interbank middle rates for 17 currencies against the Macanese pataca (MOP), back to 1986-01-02.
- Central Bank of Liberia (CBLLR) as a data provider. Daily indicative USD/LRD rates, back to 2012-07-05.
- Reserve Bank of Fiji (RBF) as a data provider. Daily mid-rates for 8 currencies against the Fijian dollar (FJD), back to 2001-01-02.
- Da Afghanistan Bank (DAB) as a data provider. Daily indicative rates for 10 currencies against the Afghan afghani (AFN), from 2019-03-31.
- Central Bank of The Gambia (CBG) as a data provider. Reference rates for 32 currencies against the Gambian dalasi (GMD), back to 2000-01-07.
- Reserve Bank of Malawi (RBM) as a data provider. Daily reference rates for ~38 currencies against the Malawian kwacha (MWK), from 2011-06-20.
- Reserve Bank of Vanuatu (RBV) as a data provider. Daily reference rates for the VUV trade-weighted basket (6 currencies) against the Vanuatu vatu (VUV), from 2025-08-26.
- Banque de la Republique du Burundi (BRB) as a data provider. Daily official rates for 19 currencies against the Burundian franc (BIF), from 2024-09-02.
- TMT (Turkmenistani manat) hard-pegged to USD at 3.5 since 2015-01-01.

### Changed

- XDR (Special Drawing Rights, ISO 4217) is now passed through consistently. BOM, NBC, and CBN rewrite their non-ISO "SDR" label to XDR; NBE no longer drops XDR. CBI and IMF already emitted XDR. BCP exposes an SDR endpoint but its page returns 100% ND, so BCP stays excluded.

### Fixed

- `/v2/rates` date-range responses are now sorted by date. Rows where carry-forward surfaced an older quote previously appeared out of order.
- `/v2/rates` no longer returns 500 errors for date/provider/quote combinations where an upstream provider published a zero rate (e.g. BNM's THB/MYR on certain 2006-2018 dates). Non-positive rates are now dropped on ingest.
- Defunct ISO 4217 codes (BGN, BYR, EEK, HRK, IEP, SLL, STD, VEF, ZMK) are no longer ingested past their retirement or redenomination date. A few providers continued publishing stale records under the old codes; these are now dropped on ingest. Run `rake db:purge_obsolete` to clean up existing rows.

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

[Unreleased]: https://github.com/lineofflight/frankfurter/compare/v2.1.1...HEAD
[2.1.1]: https://github.com/lineofflight/frankfurter/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/lineofflight/frankfurter/compare/v2.0.2...v2.1.0
[2.0.2]: https://github.com/lineofflight/frankfurter/compare/v2.0.1...v2.0.2
[2.0.1]: https://github.com/lineofflight/frankfurter/compare/v2.0.0...v2.0.1
[2.0.0]: https://github.com/lineofflight/frankfurter/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/lineofflight/frankfurter/releases/tag/v1.0.0
