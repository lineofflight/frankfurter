# Modularize exchange-rate providers and add UAH, AMD, BYN, and BWP support

## Summary

This PR does two related things:

1. extracts Frankfurter's exchange-rate ingestion into a modular provider/importer architecture
2. adds official central bank providers for currencies not covered by ECB

The branch now combines rates from multiple sources while keeping ECB authoritative for overlapping currencies. New official providers add support for:

- `UAH` via the National Bank of Ukraine (`NBU`)
- `AMD` via the Central Bank of Armenia (`CBA`)
- `BYN` via the National Bank of the Republic of Belarus (`NBRB`)
- `BWP` via the Bank of Botswana (`BOB`)

## What changed

- introduced `Bank::Provider` as the shared interface for rate providers
- extracted ECB logic into `Bank::Providers::ECB`
- added `Bank::Importer` to aggregate provider datasets for current, 90-day, historical, and saved-data imports
- updated `Bank` to import through `Bank::Importer` instead of relying on a single feed source
- kept first-provider precedence so ECB remains authoritative where currencies overlap
- added new providers for `UAH`, `AMD`, `BYN`, and `BWP`
- added importer coverage to verify cross-provider merging and overlap precedence
- added provider specs for parsing and current/historical import behavior
- fixed test setup by requiring `webmock/minitest`

## Provider details

- `NBU` fetches EUR to UAH data from the National Bank of Ukraine JSON API
- `CBA` uses the Central Bank of Armenia SOAP API and chunks historical requests
- `NBRB` imports EUR to BYN rates from the Belarus central bank JSON API
- `BOB` parses the Bank of Botswana CSV export and converts quoted EUR values into Frankfurter's EUR-base rate format

## Notes

- ECB remains the primary source for currencies it already covers
- additional providers only fill gaps, they do not overwrite ECB data
- the importer merges dates across providers and keeps the first rate seen for any overlapping currency on a given day
- provider modularity should make future currency additions much simpler

## Fixes

- Fixes #38
- Fixes #100
- Fixes #169
- Updates #144

## Not fixed by this PR

This PR does not add `AED`, `UZS`, `EGP`, `GHS`, `CRC`, `KES`, `UGX`, `NGN`, or `TZS`, so it does not fix:

- #189
- #188
- #187
- #186
- #165
- #30

## Verification

```bash
mise exec ruby@3.4.8 -- bundle exec ruby -Ispec spec/bank/importer_spec.rb
mise exec ruby@3.4.8 -- bundle exec ruby -Ispec spec/bank/providers/cba_spec.rb
mise exec ruby@3.4.8 -- bundle exec ruby -Ispec spec/bank/providers/nbrb_spec.rb
mise exec ruby@3.4.8 -- bundle exec ruby -Ispec spec/bank/providers/bob_spec.rb
```

`spec/bank/providers/nbu_spec.rb` currently fails in this branch and needs follow-up before it can be included as a passing verification step.
