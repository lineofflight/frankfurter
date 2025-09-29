# Currency Requests and Data Sources Tracker

This issue consolidates all currency requests and potential data sources for the Frankfurter API. It serves as a central tracking point for expanding beyond the current European Central Bank (ECB) data.

## Current Status

**Data Source:** European Central Bank (ECB)
**Supported Currencies:** 31 (see list below)
**Roadmap Status:** Multiple data sources planned ([see README](README.md))

### Currently Supported Currencies (via ECB)

| Code | Currency |
|------|----------|
| AUD | Australian Dollar |
| BGN | Bulgarian Lev |
| BRL | Brazilian Real |
| CAD | Canadian Dollar |
| CHF | Swiss Franc |
| CNY | Chinese Yuan |
| CZK | Czech Koruna |
| DKK | Danish Krone |
| EUR | Euro (base) |
| GBP | British Pound |
| HKD | Hong Kong Dollar |
| HUF | Hungarian Forint |
| IDR | Indonesian Rupiah |
| ILS | Israeli Shekel |
| INR | Indian Rupee |
| ISK | Icelandic Krona |
| JPY | Japanese Yen |
| KRW | South Korean Won |
| MXN | Mexican Peso |
| MYR | Malaysian Ringgit |
| NOK | Norwegian Krone |
| NZD | New Zealand Dollar |
| PHP | Philippine Peso |
| PLN | Polish Zloty |
| RON | Romanian Leu |
| SEK | Swedish Krona |
| SGD | Singapore Dollar |
| THB | Thai Baht |
| TRY | Turkish Lira |
| USD | US Dollar |
| ZAR | South African Rand |

## Requested Currencies

This table tracks currencies that have been requested by users but are not yet supported. Please update with issue references when available.

| Currency Code | Currency Name | Requested In | Potential Data Source(s) | Status | Notes |
|--------------|---------------|--------------|--------------------------|---------|-------|
| UAH | Ukrainian Hryvnia | [Test cases](spec/edge_cases_spec.rb) | National Bank of Ukraine | 🔴 Not Available | Currently returns 404 |
| *Add new requests below* | | | | | |

## Potential Data Sources

### Requirements for New Data Sources

Per our [contribution guidelines](README.md), data sources must be:
- **Non-commercial** (preferably central banks or government institutions)
- Publish **current and historical daily rates**
- Update at the **end of each working day**
- Provide data in a structured, parseable format

### Identified Data Sources

| Data Source | Type | Coverage | Update Frequency | API/Feed URL | Notes |
|------------|------|----------|------------------|--------------|-------|
| European Central Bank (ECB) | Central Bank | 31 currencies | Daily (working days) | [XML Feed](https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml) | ✅ Currently integrated |
| *Add potential sources below* | | | | | |

### Central Banks to Consider

- **National Bank of Ukraine** - For UAH
- **Bank of Russia** - For RUB (historically supported by ECB)
- **Croatian National Bank** - For HRK (historically supported by ECB)
- **Central Bank of Argentina** - For ARS
- **Reserve Bank of India** - Additional INR data
- **Bank of Thailand** - Additional THB data
- **Central Bank of Brazil** - Additional BRL data

## Implementation Priority

Priority should be based on:
1. **Frequency of requests** - How often is the currency requested?
2. **Data source reliability** - Is there a stable, non-commercial source?
3. **Geographic coverage** - Does it expand our regional coverage?
4. **User impact** - How many users would benefit?

## How to Request a New Currency

1. Check this table first to see if your currency has already been requested
2. If not listed, comment below with:
   - The currency code and name
   - Why you need this currency
   - A suggested non-commercial data source (required)
3. We'll evaluate based on the criteria above

## Related Links

- [Discussion #141](https://github.com/lineofflight/frankfurter/discussions/141) - Original discussion on currency requests and data providers
- [Roadmap](README.md#roadmap) - Multiple data sources feature
- [Contributing Guidelines](README.md#contributing) - How to contribute

## Next Steps

- [ ] Evaluate feasibility of integrating National Bank of Ukraine for UAH
- [ ] Research additional central bank APIs
- [ ] Design architecture for multiple data source support
- [ ] Implement data source abstraction layer
- [ ] Add new currencies based on user demand

---

**Note:** This is a living document. Please keep it updated as new currency requests come in or data sources are identified.

**Labels:** `enhancement`, `data-source`, `currency-request`, `help wanted`