# Parallel Test Execution & Cassette Trimming Design

## Goal

Reduce the full test suite run time from ~10.3 seconds to 2–3 seconds without deleting or weakening any test or assertion.

## Problem Analysis

The test suite currently executes 1,311 tests and 10,789 assertions in serial:
- 741 provider adapter specs take ~3.6s, parsing YAML VCR cassettes and JSON/XML/XLSX payloads.
- 570 core specs take ~6.7s, with hotspots in `blend_parity_spec.rb` (2.0s), `schedule_spec.rb` (0.8s), and `v2_spec.rb` (0.4s).
- SQLite in WAL mode prevents concurrent writes on a single database file, causing lock contention timeouts if run concurrently against one database.

## Architecture & Components

### 1. Database Isolation (`lib/db.rb`)

Support `TEST_ENV_NUMBER` set by `parallel_tests` (empty for worker 1, `"2"`, `"3"`, `"4"`, etc. for subsequent workers):

```ruby
url = ENV.fetch("DATABASE_URL") do
  env = ENV["APP_ENV"]
  worker = ENV["TEST_ENV_NUMBER"]
  suffix = worker && !worker.empty? ? "_#{worker}" : ""
  db_name = env ? "frankfurter_#{env}#{suffix}" : "frankfurter"
  "sqlite://#{Dir.pwd}/db/#{db_name}.sqlite3"
end
```

### 2. Database Preparation Task (`lib/tasks/test.rake`)

Before workers start, ensure worker database files exist:
- Seed the base `db/frankfurter_test.sqlite3` once via existing fixtures/migrations if missing or dirty.
- Flush WAL checkpoint (`PRAGMA wal_checkpoint(TRUNCATE)`).
- Copy `frankfurter_test.sqlite3` to `frankfurter_test_2.sqlite3`, `frankfurter_test_3.sqlite3`, `frankfurter_test_4.sqlite3`.
- Clean up cloned databases on task completion.

### 3. Subshell Environment Propagation (`spec/schedule_spec.rb`)

`spec/schedule_spec.rb` invokes `bin/schedule` via subshell backticks:
- Pass `DATABASE_URL` explicitly into the subshell command line or ensure `ENV["DATABASE_URL"]` is forwarded so the subshell connects to the worker's isolated database file rather than the default file.

### 4. Runner Integration (`parallel_tests`)

- Add `gem "parallel_tests", group: :test` to `Gemfile`.
- Configure `rake spec` to prepare the worker databases and run:
  ```bash
  parallel_test spec/ -n 4 --type test
  ```
- Enable `.parallel_runtime_test.log` tracking so `parallel_tests` automatically balances slow specs (e.g. `blend_parity_spec.rb`) across workers.

### 5. Cassette Trimming

Trim the top 11 largest cassettes down to the 30–50 record budget defined in `spec/vcr_cassettes/README.md`:
- `cbc.yml` (1.7 MB)
- `bcp.yml` (1.2 MB)
- `cbkkw.yml` (1.2 MB)
- `cbo.yml` (1.1 MB)
- `bi.yml` (1.0 MB)
- `brb.yml` (993 KB)
- `nbrm.yml` (853 KB)
- `bm.yml` (818 KB)
- `cbg.yml` (643 KB)
- `rbv.yml` (574 KB)
- `boja.yml` (540 KB)

Preserve all date bounds, edge cases, and ensure parsed record outputs remain identical for retained entries.

## Success Criteria

1. 1,311 runs, 10,789 assertions, 0 failures, 0 errors, 0 skips.
2. Full suite wall-clock execution time finishes in 2 to 3 seconds.
3. Code passes `bundle exec rubocop`.
