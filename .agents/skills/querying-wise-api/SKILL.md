---
name: wise-api
description: Use when querying Wise for exchange rates (real-time or historical), validating Frankfurter rates against Wise mid-market, debugging rate discrepancies, or when the user mentions Wise, sanity check, or rate comparison.
---

# Wise Exchange Rate API

Query real-time and historical mid-market rates from Wise for troubleshooting and data validation.

## Endpoints

Base: `https://api.wise.com/v1/rates`

### Current rates

```bash
# Single pair
curl -s -H "Authorization: Bearer $WISE_API_KEY" \
  'https://api.wise.com/v1/rates?source=EUR&target=USD' | jq

# All targets for a source (omit target entirely)
curl -s -H "Authorization: Bearer $WISE_API_KEY" \
  'https://api.wise.com/v1/rates?source=EUR' | jq
```

**`target` only accepts a single currency.** Comma-separated lists (`target=USD,GBP,JPY`) return `400 Bad Request`. For multiple pairs from the same source, **omit `target` to get all ~163 currencies in one call** and filter the response locally. Loop per-pair only as a last resort.

### Historical rate at a specific time

```bash
curl -s -H "Authorization: Bearer $WISE_API_KEY" \
  'https://api.wise.com/v1/rates?source=EUR&target=USD&time=2025-06-15T12:00:00' | jq
```

### Historical rate series

```bash
curl -s -H "Authorization: Bearer $WISE_API_KEY" \
  'https://api.wise.com/v1/rates?source=EUR&target=USD&from=2025-06-01&to=2025-06-30&group=day' | jq
```

## Parameters

| Param    | Description                                    | Example                          |
|----------|------------------------------------------------|----------------------------------|
| `source` | Source currency (required)                      | `EUR`                            |
| `target` | Single target currency (omit for all)           | `USD`                            |
| `time`   | Single historical timestamp (ISO 8601)          | `2025-06-15T12:00:00`           |
| `from`   | Period start (date or timestamp)                | `2025-06-01`                     |
| `to`     | Period end (date or timestamp, tz offset ok)    | `2025-06-30T23:59:59+0100`      |
| `group`  | Grouping interval for series                    | `day`, `hour`, `minute`          |

## Response

```json
[
  {
    "rate": 1.08234,
    "source": "EUR",
    "target": "USD",
    "time": "2025-06-15T12:00:00+0000"
  }
]
```

Always returns an array. Historical series returns one entry per group interval.

## Rate limit

500 requests/minute. Plenty for ad-hoc troubleshooting.

## Comparing with Frankfurter

```bash
# Frankfurter blended (multi-target OK here)
curl -s 'http://localhost:8080/v2/rates?base=EUR&quotes=USD,GBP,JPY' | jq

# Wise current: one call for all targets, filter locally
curl -s -H "Authorization: Bearer $WISE_API_KEY" \
  'https://api.wise.com/v1/rates?source=EUR' \
  | jq '.[] | select(.target | IN("USD","GBP","JPY"))'
```

Deviation: `abs(frankfurter - wise) / wise * 100`

| Deviation | Assessment                                              |
|-----------|---------------------------------------------------------|
| < 0.1%   | Excellent                                                |
| 0.1-0.5% | Acceptable (institutional vs real-time spread)           |
| > 0.5%   | Investigate — stale data or provider outlier             |
| > 1.0%   | Likely data issue — check individual provider rates      |

## Currency support

Not all currencies work as `source`. Exotic currencies (MMK, NPR, KGS, etc.) often return `400 Bad Request` when used as source. **Always use a major currency (EUR, USD, GBP) as `source` and put the exotic currency in `target`.**

```bash
# WRONG — will 400:
curl -s -H "Authorization: Bearer $WISE_API_KEY" \
  'https://api.wise.com/v1/rates?source=MMK&target=USD'

# RIGHT:
curl -s -H "Authorization: Bearer $WISE_API_KEY" \
  'https://api.wise.com/v1/rates?source=USD&target=MMK'
```

## Notes

- Wise rates are real-time; Frankfurter rates are daily institutional snapshots. Some spread is normal.
- For historical comparison, use `time` param with the date you're checking, not `from`/`to`.
- This is for internal validation only, Wise is NOT a Frankfurter provider.
- API docs: https://docs.wise.com/api-reference/rate
