# frozen_string_literal: true

require_relative "helper"
require "currency"
require "provider"
require "provider/adapters/adapter"

describe Provider do
  let(:provider) { Provider.first }

  describe "schema" do
    it "keeps series metadata on datasets" do
      _(Provider.db.schema(:providers).map(&:first)).must_equal([:key, :name, :terms_url, :country_code])
      _(Dataset.db.schema(:datasets).map(&:first)).must_include(:publish_schedule)
      _(Dataset.db.schema(:datasets).map(&:first)).must_include(:publish_cadence)
    end

    it "seeds every dataset with publish_schedule, publish_cadence, and valid cron" do
      require "fugit"
      dir = File.expand_path("../db/seeds/providers", __dir__)
      Dir["#{dir}/*.json"].each do |path|
        file = JSON.parse(File.read(path))
        (file["datasets"] || [file]).each do |data|
          _(data).must_include("publish_schedule")
          _(data).must_include("publish_cadence")
          _([nil, "daily", "weekly", "monthly", "quarterly"]).must_include(data["publish_cadence"])
          _([nil, "daily", "weekly", "monthly", "quarterly"]).must_include(data["frequency"])
          _(data["publish_cadence"].nil?).must_equal(data["publish_schedule"].nil?)
          next if data["publish_schedule"].nil?

          parsed = Fugit::Cron.parse(data["publish_schedule"])

          _(parsed).wont_be_nil("#{File.basename(path)}: invalid cron #{data["publish_schedule"].inspect}")
        end
      end
    end
  end

  describe "#adapter" do
    it "finds adapter by key" do
      stub_adapter = Class.new(Provider::Adapters::Adapter)
      Provider::Adapters.const_set(:STUB, stub_adapter)

      stub_provider = Provider.new do |p|
        p.key = "STUB"
        p.name = "Stub"
      end

      _(stub_provider.adapter).must_equal(Provider::Adapters::STUB)
    ensure
      Provider::Adapters.send(:remove_const, :STUB) if Provider::Adapters.const_defined?(:STUB)
    end

    it "resolves all seeded providers" do
      require "provider/adapters"

      Provider.all.each do |provider|
        _(Provider::Adapters.const_defined?(provider.key)).must_equal(true)
      end
    end
  end

  describe ".seed" do
    def parse(files)
      Provider.send(:parse_seeds, files)
    end

    let(:single) do
      { "key" => "TST", "name" => "Test", "pivot_currency" => "EUR", "publish_cadence" => "daily" }
    end

    let(:multi) do
      {
        "key" => "TST",
        "name" => "Test",
        "datasets" => [
          { "key" => "TST.A", "default" => true, "frequency" => "daily" },
          { "key" => "TST.B", "default" => true, "blends" => true, "frequency" => "weekly" },
          { "key" => "TST.X" },
        ],
      }
    end

    it "keeps a key only some seed files carry" do
      Provider.seed

      _(Provider["UST"].frequency).must_equal("quarterly")
      _(Provider["ECB"].frequency).must_equal("daily")
      _(Dataset.non_blending_keys).must_include("UST")
    end

    it "makes a single-series file its provider's one default dataset" do
      providers, datasets = parse("tst.json" => single)

      _(providers).must_equal([{ key: "TST", name: "Test", country_code: nil, terms_url: nil }])
      _(datasets.size).must_equal(1)
      _(datasets.first).must_equal(
        key: "TST", provider_key: "TST", is_default: true, blends: nil, rate_type: nil, frequency: "daily",
        publish_cadence: "daily", publish_schedule: nil, pivot_currency: "EUR", data_url: nil, coverage_start: nil,
      )
    end

    it "lists each dataset of a multi-series file" do
      _, datasets = parse("tst.json" => multi)

      _(datasets.map { |d| d.values_at(:key, :is_default, :blends, :frequency) }).must_equal([
        ["TST.A", true, nil, "daily"],
        ["TST.B", true, true, "weekly"],
        ["TST.X", false, nil, "daily"],
      ])
    end

    it "rejects a dataset key that is not the provider's key with a suffix" do
      multi["datasets"][2]["key"] = "TSTX"

      _ { parse("tst.json" => multi) }.must_raise(ArgumentError)
    end

    it "rejects a provider without a default dataset" do
      multi["datasets"].each { |dataset| dataset.delete("default") }

      _ { parse("tst.json" => multi) }.must_raise(ArgumentError)
    end

    it "rejects duplicate keys" do
      _ { parse("a.json" => single, "b.json" => single) }.must_raise(ArgumentError)
    end

    it "rejects unknown fields" do
      _ { parse("tst.json" => single.merge("schedule" => "* * * * *")) }.must_raise(ArgumentError)
      multi["datasets"][0]["name"] = "Table A"

      _ { parse("tst.json" => multi) }.must_raise(ArgumentError)
    end

    it "replaces nothing when a file is invalid" do
      before = [Provider.count, Dataset.count]

      Provider.stub(:parse_seeds, ->(_) { raise ArgumentError, "bad seed" }) do
        _ { Provider.seed }.must_raise(ArgumentError)
      end

      _([Provider.dataset.count, Dataset.dataset.count]).must_equal(before)
    end
  end

  describe "datasets" do
    before do
      Provider.dataset.insert(key: "TST", name: "Test")
      Dataset.dataset.multi_insert([
        { key: "TST.B", provider_key: "TST", is_default: true, frequency: "weekly", publish_cadence: "weekly",
          publish_schedule: "0 12 * * 3", coverage_start: "2020-01-01", },
        { key: "TST.A", provider_key: "TST", is_default: true, frequency: "daily", publish_cadence: "daily",
          publish_schedule: "0 12 * * 1-5", coverage_start: "2021-01-01", },
        { key: "TST.X", provider_key: "TST", is_default: false, frequency: "daily", publish_cadence: nil,
          publish_schedule: nil, coverage_start: "2019-01-01", },
      ])
      Provider.load_cache
      Dataset.load_cache
    end

    after do
      Dataset.dataset.where(provider_key: "TST").delete
      Provider.dataset.where(key: "TST").delete
      Provider.load_cache
      Dataset.load_cache
    end

    let(:provider) { Provider["TST"].dup }

    it "takes metadata from the first default dataset" do
      _(provider.datasets.map(&:key)).must_equal(["TST.A", "TST.B", "TST.X"])
      _(provider.default_datasets.map(&:key)).must_equal(["TST.A", "TST.B"])
      _(provider.publish_schedule).must_equal("0 12 * * 1-5")
      _(provider.publish_cadence).must_equal("daily")
      _(provider.frequency).must_equal("daily")
      _(provider.coverage_start).must_equal(Date.new(2019, 1, 1))
    end

    it "combines coverage across the default datasets" do
      CurrencyCoverage.dataset.multi_insert([
        { dataset_key: "TST.A", iso_code: "PLN", start_date: "2021-01-04", end_date: "2026-01-02" },
        { dataset_key: "TST.A", iso_code: "USD", start_date: "2021-01-04", end_date: "2026-01-02" },
        { dataset_key: "TST.B", iso_code: "PLN", start_date: "2020-01-08", end_date: "2025-12-31" },
        { dataset_key: "TST.B", iso_code: "AED", start_date: "2020-01-08", end_date: "2025-12-31" },
        { dataset_key: "TST.X", iso_code: "BRL", start_date: "2019-01-02", end_date: "2026-01-05" },
      ])

      _(provider.currency_coverages.map(&:iso_code).uniq.sort).must_equal(["AED", "PLN", "USD"])
      _(provider.start_date).must_equal("2020-01-08")
      _(provider.end_date).must_equal("2026-01-02")
    end

    it "resumes from the oldest dataset and restarts while any dataset has nothing" do
      Rate.dataset.multi_insert([
        { dataset_key: "TST.A", date: "2025-12-31", base: "USD", quote: "PLN", mid: 3.6 },
        { dataset_key: "TST.B", date: "2025-12-24", base: "AED", quote: "PLN", mid: 1.0 },
      ])

      _(provider.last_synced).must_be_nil

      Rate.dataset.insert(dataset_key: "TST.X", date: "2026-01-02", base: "BRL", quote: "PLN", mid: 0.7)

      _(provider.last_synced).must_equal(Date.new(2025, 12, 24))
    end

    it "stores each tagged record under its dataset" do
      tagged = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.today, base: "USD", quote: "PLN", rate: 3.6, dataset: "TST.A" },
            { date: Date.today, base: "AED", quote: "PLN", rate: 1.0, dataset: "TST.B" },
          ]
        end
      end
      provider.stub(:adapter, tagged) { provider.backfill(after: Date.today - 1) }

      _(Rate.where(dataset_key: ["TST.A", "TST.B"]).order(:base).select_map([:dataset_key, :base]))
        .must_equal([["TST.B", "AED"], ["TST.A", "USD"]])
      _(CurrencyCoverage.where(iso_code: "AED").select_map(:dataset_key)).must_equal(["TST.B"])
    end

    it "stores nothing from a provider with several datasets when a record is untagged" do
      untagged = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) { |**| [{ date: Date.today, base: "USD", quote: "PLN", rate: 3.6 }] }
      end
      Log.stub(:error, ->(_) {}) do
        provider.stub(:adapter, untagged) { provider.backfill(after: Date.today - 1) }
      end

      _(Rate.where(dataset_key: ["TST.A", "TST.B", "TST.X"]).count).must_equal(0)
    end

    it "judges missed publishes on the first default's schedule from the provider's latest observation" do
      provider.stub(:end_date, "2026-04-13") do
        _(provider.publishes_missed(reference_date: Date.new(2026, 4, 17))).must_equal(3)
      end
    end
  end

  describe "#backfill" do
    let(:provider) { Provider[key: "BCB"].dup }
    let(:fetched_params) { [] }
    let(:import_date) { Date.today }

    let(:adapter) do
      params = fetched_params
      d = import_date
      Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |after: nil, upto: nil|
          params << { after:, upto: }
          [{ date: d, base: "EUR", quote: "USD", rate: 1.1 }]
        end
      end
    end

    it "imports fetched records" do
      provider.stub(:adapter, adapter) do
        provider.backfill
      end

      record = Rate.where(dataset_key: provider.key, date: import_date).first

      _(record.base).must_equal("EUR")
      _(record.quote).must_equal("USD")
      _(record.rate).must_equal(1.1)
    end

    it "upserts without duplicating" do
      provider.stub(:adapter, adapter) do
        provider.backfill
        provider.backfill(after: Date.today - 1)
      end

      _(Rate.where(dataset_key: provider.key, date: import_date).count).must_equal(1)
    end

    describe "from coverage_start" do
      let(:coverage_start) { Date.today - 10 }

      it "fetches the coverage start day on a first backfill" do
        exclusive_adapter = Class.new(Provider::Adapters::Adapter) do
          define_method(:fetch) do |after: nil, upto: nil|
            ((after + 1)..(upto || Date.today)).map { |date| { date:, base: "EUR", quote: "USD", rate: 1.1 } }
          end
        end

        provider.stub(:coverage_start, coverage_start) do
          provider.stub(:adapter, exclusive_adapter) { provider.backfill }
        end

        _(Rate.where(dataset_key: provider.key).min(:date)).must_equal(coverage_start.to_s)
      end

      it "stores nothing dated before coverage_start" do
        # An adapter that reads after as inclusive also returns the day before coverage_start.
        stray_adapter = Class.new(Provider::Adapters::Adapter) do
          define_method(:fetch) do |after: nil, upto: nil|
            (after..(upto || Date.today)).map { |date| { date:, base: "EUR", quote: "USD", rate: 1.1 } }
          end
        end

        provider.stub(:coverage_start, coverage_start) do
          provider.stub(:adapter, stray_adapter) { provider.backfill(after: coverage_start) }
        end

        _(Rate.where(dataset_key: provider.key).min(:date)).must_equal(coverage_start.to_s)
      end
    end

    describe "when the adapter revises published values in place" do
      # HMRC may correct a monthly customs rate mid-month. If the correction replaces the row in its file rather than
      # adding one with a later start date, insert-only backfill keeps the stale figure. Surface the drift.
      let(:revising_adapter) { Class.new(adapter) { def self.revises? = true } }

      before do
        Rate.create(dataset_key: provider.key, date: import_date, base: "EUR", quote: "USD", mid: 1.0)
      end

      it "warns when a fetched value differs from the stored row" do
        logged = nil
        Log.stub(:warn, ->(message) { logged = message }) do
          provider.stub(:adapter, revising_adapter) { provider.backfill(after: import_date - 1) }
        end

        _(logged).must_include("#{provider.key}: 1 stored rate differs from source")
        _(logged).must_include("#{import_date} EUR/USD stored 1.0 fetched 1.1")
      end

      it "keeps the stored value" do
        Log.stub(:warn, ->(_) {}) do
          provider.stub(:adapter, revising_adapter) { provider.backfill(after: import_date - 1) }
        end

        _(Rate.where(dataset_key: provider.key, date: import_date, quote: "USD").first.rate).must_equal(1.0)
      end

      it "stays quiet for an adapter that does not revise" do
        logged = nil
        Log.stub(:warn, ->(message) { logged = message }) do
          provider.stub(:adapter, adapter) { provider.backfill(after: import_date - 1) }
        end

        _(logged).must_be_nil
      end
    end

    it "keeps a forward-dated row for an adapter with a publication lead" do
      ahead = Date.today + 14
      leading_adapter = Class.new(Provider::Adapters::Adapter) do
        def self.lead_days = 31

        define_method(:fetch) do |**|
          [{ date: ahead, base: "EUR", quote: "USD", rate: 1.1 }]
        end
      end

      provider.stub(:adapter, leading_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, date: ahead).count).must_equal(1)
    end

    it "retains unrecognised currency codes" do
      bad_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
            { date: Date.today, base: "EUR", quote: "SDR", rate: 1.5 },
          ]
        end
      end

      provider.stub(:adapter, bad_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, quote: "SDR").count).must_equal(1)
    end

    it "excludes non-positive rates" do
      bad_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
            { date: Date.today, base: "EUR", quote: "GBP", rate: 0.0 },
            { date: Date.today, base: "EUR", quote: "JPY", rate: -1.0 },
          ]
        end
      end

      provider.stub(:adapter, bad_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, date: Date.today, quote: "USD").count).must_equal(1)
      _(Rate.where(dataset_key: provider.key, date: Date.today, quote: "GBP").count).must_equal(0)
      _(Rate.where(dataset_key: provider.key, date: Date.today, quote: "JPY").count).must_equal(0)
    end

    it "strips float noise from synthesized rates on the way in" do
      # An adapter that coerces buy/sell to a mid, or rescales per unit, hands over the raw result of float arithmetic.
      # Single-provider responses echo stored digits verbatim, so noise stored here reaches the API (#579).
      noisy_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.today, base: "EUR", quote: "USD", rate: (181.5264 + 181.76) / 2.0 },
            { date: Date.today, base: "EUR", quote: "GBP", rate: 744.92 / 100.0 },
          ]
        end
      end

      provider.stub(:adapter, noisy_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, quote: "USD", date: Date.today).first.rate).must_equal(181.6432)
      _(Rate.where(dataset_key: provider.key, quote: "GBP", date: Date.today).first.rate).must_equal(7.4492)
    end

    it "keeps every digit a provider actually publishes" do
      precise_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [{ date: Date.today, base: "EUR", quote: "USD", rate: 0.0001234567891 }]
        end
      end

      provider.stub(:adapter, precise_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, quote: "USD", date: Date.today).first.rate).must_equal(0.0001234567891)
    end

    it "drops records dated implausibly far in the future" do
      # A single stray future-dated row (source typo, or a pre-seeded spreadsheet row) must not be stored: it would
      # hijack last_synced (= max date) and freeze incremental backfill behind an unreachable cursor.
      future_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
            { date: Date.today + 365, base: "EUR", quote: "GBP", rate: 0.85 },
          ]
        end
      end

      provider.stub(:adapter, future_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, quote: "USD", date: Date.today).count).must_equal(1)
      _(Rate.where(dataset_key: provider.key, quote: "GBP").count).must_equal(0)
    end

    it "keeps records dated within the near-future grace window" do
      # Providers in far-eastern time zones (e.g. Tonga UTC+13) and forward value-date conventions legitimately publish
      # a rate dated one day ahead of the UTC backfill clock; those must still be ingested.
      near_future_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [{ date: Date.today + 1, base: "EUR", quote: "USD", rate: 1.1 }]
        end
      end

      provider.stub(:adapter, near_future_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, quote: "USD", date: Date.today + 1).count).must_equal(1)
    end

    it "retains records dated on or after a defunct currency's terminal date" do
      defunct_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            # Retain the source's observations after BYR retired on 2016-07-01.
            { date: Date.new(2016, 7, 1), base: "EUR", quote: "BYR", rate: 22000.0 },
            { date: Date.new(2017, 1, 1), base: "BYR", quote: "USD", rate: 0.00005 },
            # Keep: before terminal date
            { date: Date.new(2016, 6, 30), base: "EUR", quote: "BYR", rate: 22000.0 },
            # Keep: unrelated record
            { date: Date.new(2016, 7, 1), base: "EUR", quote: "USD", rate: 1.1 },
          ]
        end
      end

      provider.stub(:adapter, defunct_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, quote: "BYR", date: Date.new(2016, 7, 1)).count).must_equal(1)
      _(Rate.where(dataset_key: provider.key, base: "BYR", date: Date.new(2017, 1, 1)).count).must_equal(1)
      _(Rate.where(dataset_key: provider.key, quote: "BYR", date: Date.new(2016, 6, 30)).count).must_equal(1)
      _(Rate.where(dataset_key: provider.key, quote: "USD", date: Date.new(2016, 7, 1)).count).must_equal(1)
    end

    it "leaves records for codes not in the terminal-date table unchanged" do
      neutral_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
            { date: Date.today, base: "EUR", quote: "GBP", rate: 0.85 },
          ]
        end
      end

      provider.stub(:adapter, neutral_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, date: Date.today).count).must_equal(2)
    end

    it "ingests XDR" do
      xdr_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.today, base: "EUR", quote: "USD", rate: 1.1 },
            { date: Date.today, base: "EUR", quote: "XDR", rate: 0.8 },
          ]
        end
      end

      provider.stub(:adapter, xdr_adapter) do
        provider.backfill
      end

      _(Rate.where(dataset_key: provider.key, quote: "XDR").count).must_equal(1)
    end

    it "requests a debounced cache purge when new rates are inserted" do
      cache_purged = false
      Cache.stub(:purge_debounced, -> { cache_purged = true }) do
        provider.stub(:adapter, adapter) do
          provider.backfill
        end
      end

      _(cache_purged).must_equal(true)
    end

    it "refreshes the materialized blend over the inserted window before purging" do
      calls = []
      windows = []
      Cache.stub(:purge_debounced, -> { calls << :purge }) do
        BlendedRate.stub(:refresh, lambda { |window|
          calls << :refresh
          windows << window
        },) do
          provider.stub(:adapter, adapter) do
            provider.backfill
          end
        end
      end

      _(calls).must_equal([:refresh, :purge])
      _(windows.first.begin).must_equal(import_date)
      _(windows.first.end).must_equal(import_date + CarryForward::LOOKBACK_DAYS)
    end

    it "writes blended rows for inserted dates" do
      provider.stub(:adapter, adapter) do
        provider.backfill
      end

      _(BlendedRate.where(date: import_date).count).must_be(:>, 0)
    end

    { "weekly" => "JPC", "monthly" => "BIS" }.each do |frequency, key|
      describe "with #{frequency} observations" do
        let(:provider) { Provider[key].dup }
        let(:import_date) { Date.new(2025, 1, 31) }

        it "imports source history and coverage without refreshing daily blends" do
          refreshed = []
          purged = false
          BlendedRate.stub(:refresh, ->(window) { refreshed << window }) do
            Cache.stub(:purge_debounced, -> { purged = true }) do
              provider.stub(:adapter, adapter) { provider.backfill }
            end
          end

          _(refreshed).must_be_empty
          _(Rate.where(dataset_key: key, date: import_date).first.rate).must_equal(1.1)
          _(WeeklyRate.where(dataset_key: key, bucket_date: "2025-01-29").first.rate).must_equal(1.1)
          _(MonthlyRate.where(dataset_key: key, bucket_date: "2025-01-01").first.rate).must_equal(1.1)
          coverages = CurrencyCoverage.where(dataset_key: key).order(:iso_code)

          _(coverages.select_map(:iso_code).join(",")).must_equal("EUR,USD")
          coverages.each do |coverage|
            _(coverage.start_date).must_equal(import_date)
            _(coverage.end_date).must_equal(import_date)
          end
          _(purged).must_equal(true)
        end
      end
    end

    it "does not request a cache purge when no new rates are inserted" do
      provider.stub(:adapter, adapter) do
        provider.backfill
      end

      cache_purged = false
      Cache.stub(:purge_debounced, -> { cache_purged = true }) do
        provider.stub(:adapter, adapter) do
          provider.backfill(after: import_date - 1)
        end
      end

      _(cache_purged).must_equal(false)
    end

    it "skips when already up to date" do
      Rate.dataset.insert(
        date: Date.today, dataset_key: provider.key, base: "EUR", quote: "USD", mid: 1.1,
      )

      called = false
      boom = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          called = true
          []
        end
      end

      provider.stub(:adapter, boom) do
        provider.backfill
      end

      _(called).must_equal(false)
    end

    it "chunks when adapter has backfill_range" do
      since = Date.today - 90
      Rate.dataset.insert(
        date: since, dataset_key: provider.key, base: "EUR", quote: "USD", mid: 1.0,
      )

      ranged_adapter = Class.new(adapter) do
        class << self
          def backfill_range = 30
        end
      end

      provider.stub(:adapter, ranged_adapter) do
        provider.backfill
      end

      _(fetched_params.length).must_equal(4)
      _(fetched_params[0][:after]).must_equal(since)
      _(fetched_params[0][:upto]).must_equal(since + 29)
      _(fetched_params[-1][:upto]).must_be_nil
    end

    it "refreshes currencies and currency coverages" do
      Currency.dataset.delete
      CurrencyCoverage.dataset.delete

      provider.stub(:adapter, adapter) do
        provider.backfill
      end

      _(CurrencyCoverage.where(dataset_key: provider.key).count).must_be(:>, 0)
      _(Currency.where(iso_code: "USD").count).must_equal(1)
      _(Currency.where(iso_code: "EUR").count).must_equal(1)
    end

    it "stores per-provider date ranges in coverages" do
      CurrencyCoverage.dataset.delete

      provider.stub(:adapter, adapter) do
        provider.backfill
      end

      coverage = CurrencyCoverage.where(dataset_key: provider.key, iso_code: "USD").first

      _(coverage.start_date.to_s).must_equal(import_date.to_s)
      _(coverage.end_date.to_s).must_equal(import_date.to_s)
    end

    it "contains any adapter error" do
      error_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          raise "boom"
        end
      end

      logged = nil
      Log.stub(:error, ->(message) { logged = message }) do
        provider.stub(:adapter, error_adapter) do
          provider.backfill
        end
      end

      _(logged).must_equal("#{provider.key}: RuntimeError: boom, skipping")
      _(Rate.where(dataset_key: provider.key, date: import_date).count).must_equal(0)
    end
  end
end
