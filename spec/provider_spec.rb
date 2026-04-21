# frozen_string_literal: true

require_relative "helper"
require "currency"
require "provider"
require "provider/adapters/adapter"

describe Provider do
  let(:provider) { Provider.first }

  describe "schema" do
    let(:columns) { Provider.db.schema(:providers).map(&:first) }

    it "has publish_schedule, publish_cadence, and no publish_time or publish_days" do
      _(columns).must_include(:publish_schedule)
      _(columns).must_include(:publish_cadence)
      _(columns).wont_include(:publish_time)
      _(columns).wont_include(:publish_days)
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

  describe "#publishes_missed" do
    let(:mon_fri) do
      Provider.new do |p|
        p.key = "EXAMPLE"
        p.name = "Example"
        p.publish_days = "1-5"
      end
    end

    it "returns nil when publish_days is nil" do
      provider = build_provider(publish_days: nil)

      provider.stub(:end_date, "2026-04-01") do
        _(provider.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_be_nil
      end
    end

    it "returns 0 when end_date is nil" do
      mon_fri.stub(:end_date, nil) do
        _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(0)
      end
    end

    it "returns 0 when end_date is yesterday and no publish days have elapsed" do
      # Friday 2026-04-17, reference Monday 2026-04-20: (Fri, Mon) contains only Sat/Sun.
      mon_fri.stub(:end_date, "2026-04-17") do
        _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(0)
      end
    end

    it "counts weekdays missed between end_date and reference" do
      # Last update Monday 2026-04-13, reference Friday 2026-04-17: Tue, Wed, Thu = 3 missed.
      mon_fri.stub(:end_date, "2026-04-13") do
        _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 17))).must_equal(3)
      end
    end

    it "ignores weekends for Mon-Fri providers" do
      # (2026-04-10 Fri, 2026-04-20 Mon): Mon-Fri 13-17 = 5 weekdays (18-19 weekend, 11-12 weekend).
      mon_fri.stub(:end_date, "2026-04-10") do
        _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(5)
      end
    end

    it "counts every day for providers publishing 0-6" do
      provider = build_provider(publish_days: "0-6")

      # (2026-04-10, 2026-04-20) = 9 days
      provider.stub(:end_date, "2026-04-10") do
        _(provider.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(9)
      end
    end

    it "counts only Mondays for weekly providers" do
      weekly = build_provider(publish_days: "1")

      # End_date Mon 2026-04-06, reference Fri 2026-04-24: Mondays in (6, 24) are 13, 20 = 2.
      weekly.stub(:end_date, "2026-04-06") do
        _(weekly.publishes_missed(reference_date: Date.new(2026, 4, 24))).must_equal(2)
      end
    end

    def build_provider(publish_days:)
      Provider.new do |p|
        p.key = "EXAMPLE"
        p.name = "Example"
        p.publish_days = publish_days
      end
    end
  end

  describe "#backfill" do
    let(:provider) { Provider[key: "BCB"].dup }
    let(:fetched_params) { [] }
    let(:import_date) { Date.new(2099, 1, 1) }

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

      record = Rate.where(provider: provider.key, date: import_date).first

      _(record.base).must_equal("EUR")
      _(record.quote).must_equal("USD")
      _(record.rate).must_equal(1.1)
    end

    it "upserts without duplicating" do
      provider.stub(:adapter, adapter) do
        provider.backfill
        provider.backfill(after: Date.today - 1)
      end

      _(Rate.where(provider: provider.key, date: import_date).count).must_equal(1)
    end

    it "excludes unrecognised currency codes" do
      bad_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 },
            { date: Date.new(2099, 1, 1), base: "EUR", quote: "SDR", rate: 1.5 },
          ]
        end
      end

      provider.stub(:adapter, bad_adapter) do
        provider.backfill
      end

      _(Rate.where(provider: provider.key, quote: "SDR").count).must_equal(0)
    end

    it "excludes XDR" do
      xdr_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          [
            { date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 },
            { date: Date.new(2099, 1, 1), base: "EUR", quote: "XDR", rate: 0.8 },
          ]
        end
      end

      provider.stub(:adapter, xdr_adapter) do
        provider.backfill
      end

      _(Rate.where(provider: provider.key, quote: "XDR").count).must_equal(0)
    end

    it "purges cache when new rates are inserted" do
      cache_purged = false
      Cache.stub(:purge, -> { cache_purged = true }) do
        provider.stub(:adapter, adapter) do
          provider.backfill
        end
      end

      _(cache_purged).must_equal(true)
    end

    it "does not purge cache when no new rates are inserted" do
      provider.stub(:adapter, adapter) do
        provider.backfill
      end

      cache_purged = false
      Cache.stub(:purge, -> { cache_purged = true }) do
        provider.stub(:adapter, adapter) do
          provider.backfill(after: import_date - 1)
        end
      end

      _(cache_purged).must_equal(false)
    end

    it "skips when already up to date" do
      Rate.dataset.insert(
        date: Date.today, provider: provider.key, base: "EUR", quote: "USD", rate: 1.1,
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
        date: since, provider: provider.key, base: "EUR", quote: "USD", rate: 1.0,
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

      _(CurrencyCoverage.where(provider_key: provider.key).count).must_be(:>, 0)
      _(Currency.where(iso_code: "USD").count).must_equal(1)
      _(Currency.where(iso_code: "EUR").count).must_equal(1)
    end

    it "stores per-provider date ranges in coverages" do
      CurrencyCoverage.dataset.delete

      provider.stub(:adapter, adapter) do
        provider.backfill
      end

      coverage = CurrencyCoverage.where(provider_key: provider.key, iso_code: "USD").first

      _(coverage.start_date.to_s).must_equal(import_date.to_s)
      _(coverage.end_date.to_s).must_equal(import_date.to_s)
    end

    it "skips when api key is required but missing" do
      gated_adapter = Class.new(Provider::Adapters::Adapter) do
        define_method(:fetch) do |**|
          raise Provider::Adapters::Adapter::Unavailable, "no API key"
        end
      end

      provider.stub(:adapter, gated_adapter) do
        provider.backfill
      end

      _(Rate.where(provider: provider.key, date: import_date).count).must_equal(0)
    end
  end
end
