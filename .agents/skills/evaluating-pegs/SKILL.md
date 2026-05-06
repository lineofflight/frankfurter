---
description: Use when adding, evaluating, or removing an entry in db/seeds/pegs.json, when the user mentions a pegged or fixed currency, or when an issue suggests we should "pin" or "lock" a currency to another. Also use when deciding whether to filter or override provider rates based on an assumed peg.
---

# Pegs Policy

The bar for entries in `db/seeds/pegs.json`. The file is small, load-bearing, and easy to corrupt with well-meaning additions. This skill defines what qualifies and what doesn't.

## The Bar

A currency belongs in `pegs.json` only if **all** of the following are true:

1. **Officially asserted** by an issuing authority (central bank, monetary authority, currency board, or government).
2. **Currently in force** — not historical. If a peg ended, remove the entry; don't keep it with a sunset date.
3. **A specific rate** is asserted, not a band, target, or "managed float."
4. **A primary source** exists: the issuing authority's own page, a treaty, or legislation. Wikipedia is acceptable as a fallback for long-standing colonial / dependency pegs (FKP, SHP, GGP, IMP, JEP, BMD, BTN, MOP, CVE, ANG, QAR, OMR) where the authority doesn't publish a dedicated peg page, but a central-bank URL is preferred whenever available.

If any one of these fails, the currency does **not** belong in the file.

## What Does Not Qualify

- **De facto pegs**: stable in practice but never officially declared. Example: AZN/USD has been flat at 1.7 since May 2017, but CBAR has not asserted a peg. Adding it makes us responsible if it breaks.
- **Crawling pegs / managed floats**: the rate moves within a band (e.g., CNY) or is "managed" without a fixed target.
- **Currency boards with derived rates**: if the rate is mechanically derived from another (e.g., dollarization), the currency is its base — no peg row needed unless there's a non-1 multiplier.
- **Historical pegs**: if a peg has ended, it does not belong here. Look up the current regime.
- **Forecasts or expectations**: "expected to peg" or "will likely peg." Wait until it's official.

## Schema

Each entry in `db/seeds/pegs.json`:

```json
{
  "quote": "AED",
  "base": "USD",
  "rate": 3.6725,
  "since": "1997-11-02",
  "authority": "Central Bank of the UAE",
  "source": "https://www.centralbank.ae/en/our-operations/currency-operations/exchange-rates"
}
```

All six fields are required (enforced by `Peg = Data.define(...)` in `lib/peg.rb`):

- `quote` — the pegged currency (ISO 4217)
- `base` — what it's pegged to (ISO 4217)
- `rate` — `1 base = rate quote`. Default 1.0 for parity pegs.
- `since` — date the current peg took effect, ISO 8601. Used to suppress peg-derived rates before this date.
- `authority` — the human-readable name of the asserting body.
- `source` — a stable URL. Central-bank page preferred; Wikipedia acceptable for long-standing dependency pegs.

## Adding an Entry

1. **Find the primary source.** Search the central bank or monetary authority's website. Look for "exchange rate policy," "monetary policy," or "currency arrangement." If only Wikipedia turns up something, check whether the bank's own site mentions it elsewhere — sometimes the peg is buried under a different title.
2. **Confirm the rate is current.** Some pages cite historical rates that have since been revised (e.g., Saudi riyal has had multiple regimes; the current peg dates to 1986). Match `since` to the current peg, not the original one.
3. **Add to `db/seeds/pegs.json`** in alphabetical order by `quote`.
4. **No code changes needed** — `Peg.all` reads the file directly.
5. **Add a test case** to `spec/peg_spec.rb` if the entry has unusual structure (non-1 rate, non-USD base, etc.) — for parity USD pegs, the existing tests cover it.

## Removing an Entry

If a peg is officially abandoned (e.g., 2015 CHF/EUR style), remove the entry. Do not leave it with an `until` field — the schema doesn't support one and the rate becomes wrong the moment the peg breaks.

If you discover an entry that doesn't meet the bar (a de facto peg slipped in), remove it.

## How Pegs Are Used

Pegs are treated as a source of rate data alongside providers. They contribute when the caller has not restricted the source set via `?providers=`. Two places in the codebase consume `pegs.json`:

1. **`Currency.find` / `Currency.all`** (lib/currency.rb) — synthesizes a currency record for pegged currencies that have no provider coverage of their own (e.g., FKP). Without a peg, these would not appear in `/v2/currencies`.
2. **`PegAnchor`** (lib/peg_anchor.rb) — wraps `Blender` and applies all peg behavior in one place. It substitutes the peg rate for pegged quotes (matched-base or cross-base via the peg's base as a bridge), synthesizes rows for pegged currencies that providers do not cover, and rebases output to the user's base when the request base is itself pegged.

Cross-base requests like `?base=EUR&quotes=AED` are anchored through the peg's base: the result is `blended(EUR/USD) × peg(USD→AED)` rather than `blended(EUR/AED)`. This removes provider-disagreement noise on quantities the issuing authority has fixed.

When the caller scopes via `?providers=`, `RateQuery` bypasses `PegAnchor` and uses `Blender` directly. Pegs are excluded along with all other unlisted sources, so requests like `?base=BMD&providers=ecb` (where ECB does not publish BMD) return empty rather than synthesizing peg-derived rates.

## Why the Bar Matters

Frankfurter's editorial principle is **surface uncertainty, don't impose judgement**. Adding a de facto peg means we'd be asserting a peg that the issuing authority has not. If that peg breaks, we'd be the source of stale or wrong data — not because a provider got it wrong, but because we decided what reality looked like.

The bar exists so that anything in `pegs.json` is defensible by appeal to a primary source. If a user asks "why does Frankfurter say AED is exactly 3.6725?", the answer is "because the Central Bank of the UAE says so" — not "because we observed it was usually that."

## Reference: Currently Listed Pegs

As of 2026-04, `pegs.json` contains 16 entries. All are GCC dollar pegs, GBP-area dependencies, USD-area dependencies (Caribbean), or escudo/INR/HKD pegs with treaty backing. There are no de facto pegs in the file.

If you propose an addition, check that it fits one of these established categories or has a comparably strong source.
