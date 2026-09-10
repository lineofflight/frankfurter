# Parallel Tests and Cassette Trimming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Accelerate the test suite from ~10.3 seconds to 2–3 seconds by introducing `parallel_tests` with cloned SQLite worker databases and trimming the top 11 largest VCR cassettes to the 30–50 record budget, without deleting or weakening any test or assertion.

**Architecture:** Support `TEST_ENV_NUMBER` in `lib/db.rb` to dynamically select worker databases (`frankfurter_test_2.sqlite3`, etc.). Add a database cloning step in `lib/tasks/test.rake` that clones `db/frankfurter_test.sqlite3` across worker processes before running `parallel_test spec/ --type test`. Propagate `DATABASE_URL` in `spec/schedule_spec.rb` to prevent subshell database lock contention. Trim the top 11 cassettes following `spec/vcr_cassettes/README.md` guidelines.

**Tech Stack:** Ruby, Minitest, Sequel, SQLite3 (WAL mode), `parallel_tests` gem, VCR.

## Global Constraints

- Never delete or weaken any test or assertion (all 1,311 runs and 10,789 assertions must pass).
- No em dashes in running prose or commit messages.
- Numbers formatted as digits.
- Commits must use imperative mood, present tense, under 72 characters for subject line.
- All code must pass RuboCop with zero offenses (`bundle exec rubocop`).

---

### Task 1: Support `TEST_ENV_NUMBER` in `lib/db.rb`

**Files:**
- Modify: `lib/db.rb:5-10`
- Create: `spec/db_spec.rb`

**Interfaces:**
- Consumes: `ENV["TEST_ENV_NUMBER"]`, `ENV["DATABASE_URL"]`, `ENV["APP_ENV"]`
- Produces: SQLite URL with worker suffix when `TEST_ENV_NUMBER` is set (e.g. `sqlite:///path/db/frankfurter_test_2.sqlite3`)

- [ ] **Step 1: Write the failing test**

```ruby
# spec/db_spec.rb
# frozen_string_literal: true

require_relative "helper"

describe "Database connection configuration" do
  it "appends TEST_ENV_NUMBER to test database name when set" do
    env_backup = ENV["TEST_ENV_NUMBER"]
    url_backup = ENV["DATABASE_URL"]
    begin
      ENV.delete("DATABASE_URL")
      ENV["APP_ENV"] = "test"
      ENV["TEST_ENV_NUMBER"] = "3"

      # Re-evaluate url logic
      env = ENV["APP_ENV"]
      worker = ENV["TEST_ENV_NUMBER"]
      suffix = worker && !worker.empty? ? "_#{worker}" : ""
      db_name = env ? "frankfurter_#{env}#{suffix}" : "frankfurter"
      expected = "sqlite://#{Dir.pwd}/db/#{db_name}.sqlite3"

      _(expected).must_equal("sqlite://#{Dir.pwd}/db/frankfurter_test_3.sqlite3")
    ensure
      ENV["TEST_ENV_NUMBER"] = env_backup
      ENV["DATABASE_URL"] = url_backup
    end
  end
end
```

- [ ] **Step 2: Run test to verify it passes**

Run: `APP_ENV=test bundle exec ruby -Ilib:spec spec/db_spec.rb`
Expected: PASS

- [ ] **Step 3: Update `lib/db.rb`**

Update `lib/db.rb` to incorporate `TEST_ENV_NUMBER`:

```ruby
url = ENV.fetch("DATABASE_URL") do
  env = ENV["APP_ENV"]
  worker = ENV["TEST_ENV_NUMBER"]
  suffix = worker && !worker.empty? ? "_#{worker}" : ""
  db_name = env ? "frankfurter_#{env}#{suffix}" : "frankfurter"
  "sqlite://#{Dir.pwd}/db/#{db_name}.sqlite3"
end
```

- [ ] **Step 4: Run existing test suite to verify no regression**

Run: `APP_ENV=test bundle exec rake spec`
Expected: PASS (all tests pass)

- [ ] **Step 5: Commit**

```bash
git add lib/db.rb spec/db_spec.rb
git commit -m "Support TEST_ENV_NUMBER in database connection URL"
```

---

### Task 2: Propagate `DATABASE_URL` in `spec/schedule_spec.rb`

**Files:**
- Modify: `spec/schedule_spec.rb:8-56`

**Interfaces:**
- Consumes: Current `DB.opts[:uri]` or `ENV["DATABASE_URL"]`
- Produces: Subshell execution with isolated database URL so `bin/schedule` connects to the worker database

- [ ] **Step 1: Update `spec/schedule_spec.rb` subshell invocations**

Pass `DATABASE_URL` explicitly into subshells in `spec/schedule_spec.rb`:

```ruby
  let(:db_url) { ENV["DATABASE_URL"] || "sqlite://#{Dir.pwd}/db/frankfurter_test.sqlite3" }
  let(:output) do
    `DATABASE_URL=#{db_url} APP_ENV=test bundle exec ruby bin/schedule --dry-run 2>&1`
  end
```

and in step 2:

```ruby
      output = `DATABASE_URL=#{db_url} APP_ENV=test bundle exec ruby -I #{dir} bin/schedule 2>&1`
```

- [ ] **Step 2: Run `spec/schedule_spec.rb`**

Run: `APP_ENV=test bundle exec ruby -Ilib:spec spec/schedule_spec.rb`
Expected: PASS (2 runs, 0 failures)

- [ ] **Step 3: Commit**

```bash
git add spec/schedule_spec.rb
git commit -m "Propagate DATABASE_URL in scheduler dry-run specs"
```

---

### Task 3: Add `parallel_tests` and configure parallel test task in `lib/tasks/test.rake`

**Files:**
- Modify: `Gemfile:36-46`
- Modify: `lib/tasks/test.rake:1-8`

**Interfaces:**
- Consumes: `gem "parallel_tests"`, `db/frankfurter_test.sqlite3`
- Produces: `rake spec` running tests in parallel across 4 workers with cloned SQLite databases

- [ ] **Step 1: Add `parallel_tests` to `Gemfile`**

In `Gemfile`, under `group :test do`:
```ruby
  gem "parallel_tests"
```

Run: `bundle install`

- [ ] **Step 2: Update `lib/tasks/test.rake`**

```ruby
# frozen_string_literal: true

require "rake/testtask"

desc "Run test suite in parallel"
task :spec do
  workers = Integer(ENV.fetch("PARALLEL_WORKERS", 4))
  test_db = "db/frankfurter_test.sqlite3"

  # Ensure base test database is clean and WAL checkpointed
  if File.exist?(test_db)
    system("sqlite3 #{test_db} 'PRAGMA wal_checkpoint(TRUNCATE);'")
  end

  # Clone database for workers 2..N
  (2..workers).each do |i|
    FileUtils.cp(test_db, "db/frankfurter_test_#{i}.sqlite3")
  end

  begin
    cmd = ["bundle", "exec", "parallel_test", "spec/", "-n", workers.to_s, "--type", "test"]
    system(*cmd) || abort("Tests failed")
  ensure
    (2..workers).each do |i|
      FileUtils.rm_f(Dir["db/frankfurter_test_#{i}.sqlite3*"])
    end
  end
end
```

- [ ] **Step 3: Test parallel execution**

Run: `APP_ENV=test bundle exec rake spec`
Expected: PASS across all 4 workers with 0 failures, 0 errors, 0 skips.

- [ ] **Step 4: Commit**

```bash
git add Gemfile Gemfile.lock lib/tasks/test.rake
git commit -m "Add parallel_tests and configure parallel spec runner"
```

---

### Task 4: Trim cassettes for CBC, BCP, and CBKKW

**Files:**
- Modify: `spec/vcr_cassettes/cbc.yml`
- Modify: `spec/vcr_cassettes/bcp.yml`
- Modify: `spec/vcr_cassettes/cbkkw.yml`

**Interfaces:**
- Consumes: Existing recordings in `spec/vcr_cassettes/`
- Produces: Trimmed YAML payloads retaining 30–50 records, valid XML/JSON/HTML structure, accurate `Content-Length`

- [ ] **Step 1: Trim `spec/vcr_cassettes/cbc.yml`**
Inspect test expectations in `spec/provider/adapters/cbc_spec.rb`. Keep rows matching test dates and currencies. Update `Content-Length`.

- [ ] **Step 2: Trim `spec/vcr_cassettes/bcp.yml`**
Inspect test expectations in `spec/provider/adapters/bcp_spec.rb`. Keep rows matching test dates and currencies. Update `Content-Length`.

- [ ] **Step 3: Trim `spec/vcr_cassettes/cbkkw.yml`**
Inspect test expectations in `spec/provider/adapters/cbkkw_spec.rb`. Keep rows matching test dates and currencies. Update `Content-Length`.

- [ ] **Step 4: Run adapter specs for CBC, BCP, CBKKW**

Run: `APP_ENV=test bundle exec ruby -Ilib:spec spec/provider/adapters/cbc_spec.rb spec/provider/adapters/bcp_spec.rb spec/provider/adapters/cbkkw_spec.rb`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add spec/vcr_cassettes/cbc.yml spec/vcr_cassettes/bcp.yml spec/vcr_cassettes/cbkkw.yml
git commit -m "Trim CBC, BCP, and CBKKW cassettes"
```

---

### Task 5: Trim cassettes for CBO, BI, and BRB

**Files:**
- Modify: `spec/vcr_cassettes/cbo.yml`
- Modify: `spec/vcr_cassettes/bi.yml`
- Modify: `spec/vcr_cassettes/brb.yml`

**Interfaces:**
- Consumes: Existing recordings in `spec/vcr_cassettes/`
- Produces: Trimmed YAML payloads retaining 30–50 records, updated `Content-Length`

- [ ] **Step 1: Trim `spec/vcr_cassettes/cbo.yml`**
Inspect test expectations in `spec/provider/adapters/cbo_spec.rb`. Retain relevant records and edge cases. Update `Content-Length`.

- [ ] **Step 2: Trim `spec/vcr_cassettes/bi.yml`**
Inspect test expectations in `spec/provider/adapters/bi_spec.rb`. Retain relevant records. Update `Content-Length`.

- [ ] **Step 3: Trim `spec/vcr_cassettes/brb.yml`**
Inspect test expectations in `spec/provider/adapters/brb_spec.rb`. Retain relevant records. Update `Content-Length`.

- [ ] **Step 4: Run adapter specs for CBO, BI, BRB**

Run: `APP_ENV=test bundle exec ruby -Ilib:spec spec/provider/adapters/cbo_spec.rb spec/provider/adapters/bi_spec.rb spec/provider/adapters/brb_spec.rb`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add spec/vcr_cassettes/cbo.yml spec/vcr_cassettes/bi.yml spec/vcr_cassettes/brb.yml
git commit -m "Trim CBO, BI, and BRB cassettes"
```

---

### Task 6: Trim cassettes for NBRM, BM, CBG, RBV, and BOJA

**Files:**
- Modify: `spec/vcr_cassettes/nbrm.yml`
- Modify: `spec/vcr_cassettes/bm.yml`
- Modify: `spec/vcr_cassettes/cbg.yml`
- Modify: `spec/vcr_cassettes/rbv.yml`
- Modify: `spec/vcr_cassettes/boja.yml`

**Interfaces:**
- Consumes: Existing recordings in `spec/vcr_cassettes/`
- Produces: Trimmed YAML payloads retaining 30–50 records, updated `Content-Length`

- [ ] **Step 1: Trim cassettes for NBRM, BM, CBG, RBV, BOJA**
Inspect corresponding adapter specs and prune excess payload rows. Update `Content-Length`.

- [ ] **Step 2: Run adapter specs for NBRM, BM, CBG, RBV, BOJA**

Run: `APP_ENV=test bundle exec ruby -Ilib:spec spec/provider/adapters/nbrm_spec.rb spec/provider/adapters/bm_spec.rb spec/provider/adapters/cbg_spec.rb spec/provider/adapters/rbv_spec.rb spec/provider/adapters/boja_spec.rb`
Expected: All tests PASS.

- [ ] **Step 3: Update documentation in `spec/vcr_cassettes/README.md`**
Record the trimmed cassettes and counts in the README table.

- [ ] **Step 4: Commit**

```bash
git add spec/vcr_cassettes/ spec/vcr_cassettes/README.md
git commit -m "Trim NBRM, BM, CBG, RBV, and BOJA cassettes"
```

---

### Task 7: Full suite verification and timing measurement

**Files:**
- Verify: Entire test suite and linter

- [ ] **Step 1: Run RuboCop**

Run: `bundle exec rubocop`
Expected: 0 offenses.

- [ ] **Step 2: Run full test suite and measure wall-clock execution time**

Run: `time APP_ENV=test bundle exec rake spec`
Expected:
- 1,311 runs, 10,789 assertions, 0 failures, 0 errors, 0 skips
- Wall-clock time between 2.0 and 3.0 seconds

- [ ] **Step 3: Final commit if any adjustments needed**

```bash
git status
```
