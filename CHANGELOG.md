# Changelog

All notable changes to the Frankfurter API will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- V2 daily date ranges of any length, including full history, are now served in seconds. Blended rates are materialized into a table refreshed on every data arrival, so range responses read precomputed values instead of recomputing the blend per date. The 5-year cap introduced as an interim guard is lifted for these requests and remains only for `providers=` and `expand=providers` ranges, which still compute live. (#569, #570)
- Three deliberate corrections accompany the materialization, each trading a per-shape inconsistency for one canonical value. First, the snapped-back row at the start of a range (when the range starts on a day without data) now always carries the value blended at the row's own observation date; previously the same dated row could differ depending on where the range started, because a contributing source could age out of the carry-forward window between those dates. By the same principle, an observation the consensus filter rejected at its own date no longer resurfaces retroactively once the sources that outvoted it age out of the window (one redenomination-era record in the full history behaved this way), and rebasing a range to a non-USD base divides by that base's value blended at its own observation date rather than a value re-weighted at each later response date. Relatedly, range queries combining `providers=` with a pegged base currency now return empty, matching the documented single-date behavior: restricting the source set bypasses the peg layer, so there is no anchor to rebase through. Second, range responses always blend through the USD pivot; previously a range whose contributing rows all shared the requested base blended directly in that base, producing slightly different values for the same data depending on the request shape. Third, `quotes` is now purely a row filter: it no longer changes blended values. Previously the filter narrowed the fetch by matching each provider's registered pivot currency, which silently dropped contributors whose rows do not involve that pivot (Bank of Lithuania's post-euro rows, Banco Central de Bolivia's USD-quoted metals), so a filtered request could disagree with an unfiltered one about the same pair, for example EUR/USD 1.1459 versus 1.1458 on the same day. (#570)
- V2 date ranges spanning more than 5 years at daily granularity using `providers=` or `expand=providers` return 422. With `providers=` naming at most 5 providers, a `quotes` list of at most 5 currencies lifts the cap, since the provider filter bounds the work; a provider-unbounded `expand=providers` range computes every currency regardless of `quotes`, so there the ways out are `group=week` or `group=month` aggregation, adding `providers=`, or splitting the range into shorter requests. The same cap also protects plain daily ranges on installations whose blend table has not been materialized yet; the scheduler builds it automatically within minutes of first boot. (#569, #570)
- V2 range requests now enforce the request deadline inside blend computation, between range chunks. Previously the deadline was only checked between streamed response chunks, which the server buffers as fast as they are produced, so a heavy range could compute at full cost long past its timeout without ever delivering a response. A deadline that expires before streaming begins now returns 503. (#569)
- CDN cache purges triggered by provider backfills are now debounced to at most one purge per five-minute window, with a trailing purge so the last insert of a backfill wave still becomes visible. Previously every provider insert purged the entire CDN cache, and the post-deploy backfill burst could send all heavy uncached queries to the origin at once. (#568)
- Cross-rate conversion during blending now resolves bridge rates through a per-group hash index instead of rescanning the group per row. Long-range multi-provider queries spend the bulk of their time in this path; a full-history export runs about 25% faster, which matters most right after a CDN cache purge when heavy exports all recompute at once.
- Provider HTTP errors no longer record silent no-data days; every non-2xx response now fails the fetch loudly and is retried on the next scheduled run.
- Adapter parsers no longer convert alien 200 responses (a maintenance page, a homepage standing in for a retired file, a restructured API) into silent no-data days. An audit of all 81 defensive empty returns across 48 provider adapters converted structural guards (a missing rates table, JSON envelope, or workbook sheet) into loud failures that abort the run for retry, and narrowed the legitimately-empty cases (holiday pages, image-only scans, empty-year responses) to observed no-data markers, each documented in place and verified against live provider responses. (#563)
- V2 latest rates now consider legitimate provider observations dated one day ahead of the service date, so next-day official rates are visible as soon as they are published instead of appearing only after the UTC date rolls over. Explicit date and range queries keep their requested date boundaries.

### Fixed

- Restored State Bank of Pakistan (SBP) rates, stalled since late May 2026 after the bank retired its /ecodata/ file tree in a site restructure and both workbook URLs began serving the homepage as HTTP 200. The adapter now fetches the same workbooks from their new /assets/document/ location, closing the gap from June 2026 onward. (#564)
- Fixed the Docker image healthcheck restarting the container in a loop under load. The old 2s probe ran curl under a shell child; when docker killed a timed-out probe, the orphaned curl was reaped by foreman (PID 1), which tore the container down. The probe now execs curl directly with its own 9s cap, so a timeout leaves nothing to orphan, and the relaxed cadence (30s interval, 5m start period for upgrade migrations) stops probe kills from arising in the first place. Passing `--init` to `docker run` remains recommended as defense in depth. (#556)
- Provider adapters that fetch over raw HTTP now check the response status before parsing. An error page (a WAF block, a CDN 500) contains no rate table, so parsing it produced an empty result indistinguishable from a genuine no-data day; incremental backfill then advanced past the date and never revisited it, leaving a permanent gap. Affected runs now abort and retry on the next scheduled tick. National Bank of Cambodia (NBC) lost 2026-06-30, 2026-07-03 and 2026-07-15 this way before the fix. (#555)
- Restored Banque Centrale de Tunisie (BCT) rates, which stopped updating in early July 2026 after the source's CDN began returning HTTP 500 for the default library User-Agent. Requests now send a non-library User-Agent, matching the workaround already used for other CDN-fronted providers. (#548)
- Banco Central de Bolivia (BCBO) ingestion resumes after the source replaced its daily-sheet layout in mid-2026 (ISO code moved to a new column, the USD VENTA/COMPRA split collapsed into a single official rate, metals and SDR moved into their own blocks). The adapter now detects and parses both the current and legacy layouts, so historical daily data remains re-backfillable. BOB rates reflect Bolivia's mid-2026 repricing of the boliviano off its long-standing peg. (#547)
- Date-relative V2 responses (latest, or `from` without `to`) now expire from CDN caches at UTC midnight instead of after 24 hours. Previously a cached "latest" response could disagree with a freshly computed open-ended range for the same data after the UTC date rolled over, since cache purges only fire when new data arrives. (#541)

### Added

- V2 `expand=providers` entries now include each provider's observation `date` alongside its key and rate.
- V2 rate responses now include an identity record for the base currency (base equals quote, rate 1), so clients can index or iterate over currencies without special-casing the base. The record obeys the `quotes` filter like any other row: it appears by default and when the base is listed in `quotes`, and is omitted when `quotes` excludes the base. Same-currency pairs like `/v2/rate/USD/USD` now return 1 instead of 404. (#538)

## [2.3.5] - 2026-06-25

### Changed

- Bangko Sentral ng Pilipinas (BSP) now contributes only its official USD/PHP Reference Rate. The rest of the daily bulletin was third-party data BSP reprints: a peso-equivalent table of ~32 currencies from LSEG (Refinitiv) closing prices, plus SDR (IMF) and gold/silver (LBMA) quotes. Those pairs are already covered with ample independent depth by official sources in the blend, and relaying them attributed correlated, internally inconsistent data to BSP (the table is anchored on a USD/PHP that differs from the Reference Rate). Blended rates are unaffected; single-provider BSP queries now return only USD/PHP. (#533)

## [2.3.4] - 2026-06-25

### Fixed

- Single-provider V2 queries now preserve the source's own precision for rates it publishes directly. Responses were previously rounded to standard FX magnitude bands, which trimmed the extra decimals a few providers carry on high-magnitude pairs (for example, BSP's GBP/PHP and CHF/PHP). Blended rates and derived cross-rates are still rounded, since their additional digits are an artefact of averaging or conversion. (#534)

## [2.3.3] - 2026-06-22

### Fixed

- Restored Reserve Bank of Vanuatu (RBV) rates, which had silently stopped updating in late May. The source began serving a TLS certificate chain missing its intermediate, which Ruby (unlike some browsers) rejects; the intermediate is now bundled and supplied at request time, so the chain verifies without disabling certificate checks.
- Restored Central Bank of Samoa (CBS) rates, which had stopped updating after the source changed its workbook filename to a space-separated form (e.g. `Historical Daily Rates-220626.xlsx`). The unescaped space raised a URI error on every run; the scraped link is now percent-encoded before download.
- Pre-1999 euro rates sourced from Sveriges Riksbank (RB), which backfills its EUR series with the ECU, are now labelled with the ECU's ISO code (XEU) rather than EUR — matching how other providers (BdP, AMCM) report the same data.
- Euro-denominated rates dated before the euro existed (1999-01-04) are no longer ingested, so euro crosses for legacy currencies (e.g. ATS/EUR, DEM/EUR) now correctly begin in 1999. The earlier history remains available against the ECU (XEU) and contemporaneous currencies such as USD and DEM. Run `rake db:purge_invalid` to remove any such rows already stored.
- The euro-legacy currencies ATS, BEF, DEM, ESP, FRF, ITL, NLG, and PTE are now retired at their respective euro changeover dates (joining the Irish pound, IEP, already handled). Stale rates that some providers published for these currencies as late as 2004 are no longer ingested. Run `rake db:purge_invalid` to remove any such rows already stored.
- Weekly and monthly time-series no longer omit the current, in-progress period. The purge of future-dated rows compared each rollup's bucket date against the daily cutoff, so the live week's or month's bucket — which anchors to a past Monday or the first of the month — could be dropped; the cutoff is now applied per rollup period. (#521)

## [2.3.2] - 2026-06-13

### Fixed

- Rates dated implausibly far in the future are no longer ingested. A stray upstream date was being stored as a provider's most recent rate, which silently froze that provider's incremental updates until the bogus date arrived; the National Reserve Bank of Tonga (NRBT) and Reserve Bank of Vanuatu (RBV) feeds were affected. Run `rake db:purge_invalid` to remove any such rows already stored.

## [2.3.1] - 2026-06-11

### Fixed

- Docker containers no longer terminate on startup when the scheduler staggers provider backfills. (#514)

## [2.3.0] - 2026-06-11

### Added

- Banco Central de Bolivia (BCBO) now supports a daily multi-currency basket of ~50 currencies, plus daily reference prices for gold (XAU), silver (XAG), and SDR (XDR) from 2008-01-01 onwards. Historical USD/BOB rates prior to 2008 continue to be sourced from the yearly XLS archive.
- Docker builds now support multi-architecture target platforms, building and publishing both `linux/amd64` and `linux/arm64` images to Docker Hub under a single manifest list. (#140)

### Fixed

- Long `/v2/rates` time-series exports (including `.csv`) are now much faster. Each day in the range was being rebuilt by rescanning the entire window; a sliding window now reuses that work, cutting generation time for multi-year ranges by roughly 3x. Output is unchanged.
- Time-series exports now send `stale-while-revalidate` and `stale-if-error` cache directives, so the edge keeps serving the last good export while it revalidates or if the origin errors, instead of returning a failure.
- `/v2/rates` no longer returns a 500 for date ranges spanning December 2014, which previously broke full-history exports (every currency's history crosses that window). Banque du Liban changed its quote currency from the Lithuanian litas to the euro around Lithuania's 2015 euro adoption, leaving overlapping rows that reach the same currency two ways; these are now reconciled into one rate instead of failing the request.
- Restored Central Bank of Samoa (CBS) rates, which had stopped updating after the source moved its workbook to a date-stamped filename that changes daily. The link is now read from the data page on each run instead of being hardcoded.

## [2.2.0] - 2026-06-01

### Added

- Bangko Sentral ng Pilipinas (BSP) as a data provider. Daily Reference Exchange Rate Bulletin rates for ~32 currencies against the Philippine peso (PHP), from 2017-11-06. USD uses BSP's published reference rate (mid). The bulletin's USD-denominated SDR rate (XDR) and gold/silver buying prices (XAU/XAG per troy ounce) are also included, in their native USD direction.
- Banco Central de Bolivia (BCBO) as a data provider. The daily official rate (tipo de cambio oficial) for USD against the boliviano (BOB), with buy/sell coerced to mid, from 2000-01-01.
- `/v2/providers` now includes `publish_cadence` (`daily`, `weekly`, `monthly`, or `null`) for each provider. It gives the unit for `publishes_missed`, so consumers can tell whether a provider is one day, week, or month behind.

### Fixed

- CBC no longer reports a false "missed publishes" count. Its multi-currency feed is refreshed in monthly batches in arrears, not daily, so it is now treated as a monthly publisher.
- Restored Bank of Tanzania (BOTA) rates, which had stopped updating after the source added antiforgery token validation to its rates endpoint.

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
- Defunct ISO 4217 codes (BGN, BYR, EEK, HRK, IEP, SLL, STD, VEF, ZMK) are no longer ingested past their retirement or redenomination date. A few providers continued publishing stale records under the old codes; these are now dropped on ingest. Run `rake db:purge_invalid` to clean up existing rows.

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

[Unreleased]: https://github.com/lineofflight/frankfurter/compare/v2.3.3...HEAD
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
