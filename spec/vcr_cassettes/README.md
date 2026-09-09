# VCR cassettes

Record real responses with fixed `after:` and `upto:` dates, usually 3–5 days apart.
For feeds that return whole archives, trim the recorded payload to at most 100
parsed rate records per response. Aim for 30–50 when coverage allows it.

Retain published values, headers, relevant sheets and representative edge cases.
Keep dates outside the requested bounds so filtering assertions still prove
something. Preserve missing values, hidden rows, unit changes and overlapping
sources where the tests depend on them. Update `Content-Length` after trimming.

Compare parsed records before and after: every retained record must match the
original recording. Run the existing suite without weakening its assertions:

```sh
APP_ENV=test bundle exec rake
```

The September 2026 workbook sweep retained:

| Cassette | Parsed records | Cases retained |
| --- | ---: | --- |
| BOZ | 48 | Hidden placeholders, first fixing, redenomination, date bounds |
| SBP archive / current | 81 / 35 | All 23 currencies, overlapping sources, date bounds, June archive |
| CBSSC archive | 29 | All 3 currencies, first fixing, missing fixing, date bounds; live response unchanged |
| CBS | 90 | All 9 currencies, month headers, date bounds |
| BDL | 77 | All 7 currencies and 3 sheets, duplicate rows, rate change, date bounds |

Other existing cassettes may exceed the limit. Profile with
`bundle exec rake spec TESTOPTS=--verbose` before choosing the next ones to trim.
