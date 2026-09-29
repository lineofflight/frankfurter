# frozen_string_literal: true

# Runs the Ruby blend and rollup tasks on the spec fixture plus edge rows, and writes their input and output tables as
# gzipped JSON for the Go parity test in internal/blend (TestGoldenTasks).
#
#   DATABASE_URL=sqlite://<scratch>/blend.sqlite3 APP_ENV=test \
#     mise exec -- bundle exec ruby -Ilib -r./boot go/scripts/blend_golden.rb 2026-09-29 \
#     go/internal/blend/testdata/golden/tasks.json.gz
#
# The date pins Date.today, so the fixture and every task see the same day whenever the file is regenerated. The
# database must be a throwaway file: the script migrates it and replaces its contents. Stages, each dumped:
#
#   1. rollups:rebuild (all providers) after seeding
#   2. blend:rebuild
#   3. rollups:rebuild[ecb] after ECB's GBP rates move 1%

require "date"
require "json"
require "zlib"

abort "blend_golden.rb: run with APP_ENV=test" unless ENV["APP_ENV"] == "test"
url = ENV.fetch("DATABASE_URL", "")
abort "blend_golden.rb: point DATABASE_URL at a throwaway sqlite file" unless url.start_with?("sqlite://")
abort "usage: blend_golden.rb YYYY-MM-DD OUT.json.gz" unless ARGV.size == 2

TODAY = Date.parse(ARGV[0])
Date.singleton_class.define_method(:today) { TODAY }

# Migrate in a child process: older data migrations load Provider before the frequency column exists, and a Provider
# class defined then would never exclude non-blending providers in this process.
migrate = 'require "db"; Sequel.extension(:migration); Sequel::Migrator.run(DB, "db/migrate")'
system(RbConfig.ruby, "-Ilib", "-r./boot", "-e", migrate, exception: true)

require "db"

require_relative "../../spec/fixtures"
require "blended_rate"
require "blended_weekly_rate"
require "blended_monthly_rate"
require "cache"
require "rake"
Cache.define_singleton_method(:purge) {}
load "lib/tasks/rollups.rake"
load "lib/tasks/blend.rake"

Fixtures.seed!

def insert(provider, date, base, quote, mid: nil, bid: nil, ask: nil)
  Rate.dataset.insert(provider:, date:, base:, quote:, mid:, bid:, ask:)
end

# Aging: T2's stale MXN ages out of the lookback before later anchors.
observed = Fixtures.business_day(40)
stale = observed - 10
insert("T1", observed, "EUR", "MXN", mid: 20.0)
insert("T1", observed, "EUR", "USD", mid: 1.2)
insert("T2", stale, "EUR", "MXN", mid: 40.0)
insert("T2", stale, "EUR", "USD", mid: 1.2)

# Consensus: X's ZAR is an outlier while the cohort is in the lookback, then emerges alone.
d0 = Fixtures.business_day(80)
[["C1", 19.0], ["C2", 19.1], ["C3", 18.9], ["C4", 19.05]].each do |provider, rate|
  insert(provider, d0, "EUR", "ZAR", mid: rate)
  insert(provider, d0, "EUR", "USD", mid: 1.08)
end
insert("X", d0 + 9, "EUR", "ZAR", mid: 99.0)
insert("X", d0 + 9, "EUR", "USD", mid: 1.08)

# Two bridges to the same quote, collapsed by BaseConversion#reconcile.
[Fixtures.business_day(100), Fixtures.business_day(107)].each do |date|
  insert("IMF", date, "EUR", "USD", mid: 1.16)
  insert("IMF", date, "USD", "XDR", mid: 0.73)
  insert("IMF", date, "EUR", "XDR", mid: 0.85)
end

# A non-blending provider: in rollups, never in blends.
[Fixtures.business_day(10), Fixtures.business_day(17)].each do |date|
  insert("UST", date, "USD", "XDR", mid: 0.75)
  insert("UST", date, "USD", "MXN", mid: 17.0)
end

# Future-dated rows cap the weighted average's reference date at today.
insert("T3", TODAY + 1, "EUR", "GEL", mid: 3.0)
insert("T3", TODAY + 1, "EUR", "USD", mid: 1.09)
insert("T3", TODAY - 3, "EUR", "GEL", mid: 2.9)
insert("T3", TODAY - 3, "EUR", "USD", mid: 1.1)

# A pegged quote with provider rows; the peg overrides the blend.
insert("T4", Fixtures.business_day(5), "USD", "AED", mid: 3.7)

# Bid and ask only: the stored rate is their midpoint.
insert("T4", Fixtures.business_day(6), "EUR", "USD", bid: 1.07, ask: 1.09)
insert("T4", Fixtures.business_day(6), "EUR", "SEK", bid: 11.1, ask: 11.3)

# A defunct currency around its terminal date, and an unknown code.
[Date.new(2022, 12, 29), Date.new(2022, 12, 30), Date.new(2023, 1, 2), Date.new(2023, 1, 3)].each do |date|
  insert("T5", date, "EUR", "HRK", mid: 7.53)
  insert("T5", date, "EUR", "USD", mid: 1.07)
  insert("T5", date, "EUR", "XYZ", mid: 2.0)
end

def dump(table, columns, order)
  DB[table].order(*order).select_map(columns).map do |row|
    row.map { |v| v.is_a?(Date) ? v.to_s : v }
  end
end

def grouped
  {
    weekly_rates: dump(:weekly_rates, [:bucket_date, :provider, :base, :quote, :rate],
      [:bucket_date, :provider, :base, :quote],),
    monthly_rates: dump(:monthly_rates, [:bucket_date, :provider, :base, :quote, :rate],
      [:bucket_date, :provider, :base, :quote],),
    blended_weekly_rates: dump(:blended_weekly_rates, [:bucket_date, :quote, :rate], [:bucket_date, :quote]),
    blended_monthly_rates: dump(:blended_monthly_rates, [:bucket_date, :quote, :rate], [:bucket_date, :quote]),
  }
end

def invoke(name, *args)
  Rake::Task[name].reenable
  Rake::Task[name].invoke(*args)
end

out = {
  today: TODAY.to_s,
  rates: dump(:rates, [:date, :provider, :base, :quote, :mid, :bid, :ask], [:date, :provider, :base, :quote]),
}

invoke("rollups:rebuild")
out[:rollups] = grouped

invoke("blend:rebuild")
out[:blend] = grouped.merge(blended_rates: dump(:blended_rates, [:date, :quote, :rate], [:date, :quote]))

Rate.where(provider: "ECB", quote: "GBP").update(mid: Sequel[:mid] * 1.01)
invoke("rollups:rebuild", "ecb")
out[:ecb] = grouped

Zlib::GzipWriter.open(ARGV[1]) { |gz| gz.write(JSON.generate(out)) }
