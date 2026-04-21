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

    it "seeds every provider with publish_schedule, publish_cadence, and valid cron" do
      require "fugit"
      dir = File.expand_path("../db/seeds/providers", __dir__)
      Dir["#{dir}/*.json"].each do |path|
        data = JSON.parse(File.read(path))

        _(data).must_include("publish_schedule")
        _(data).must_include("publish_cadence")
        _(data).wont_include("publish_time")
        _(data).wont_include("publish_days")
        _([nil, "daily", "weekly", "monthly"]).must_include(data["publish_cadence"])
        _(data["publish_cadence"].nil?).must_equal(data["publish_schedule"].nil?)
        next if data["publish_schedule"].nil?

        parsed = Fugit::Cron.parse(data["publish_schedule"])

        _(parsed).wont_be_nil("#{File.basename(path)}: invalid cron #{data["publish_schedule"].inspect}")
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

  describe "#publishes_missed" do
    def build_provider(schedule, cadence: "daily")
      Provider.new do |p|
        p.key = "EXAMPLE"
        p.name = "Example"
        p.publish_schedule = schedule
        p.publish_cadence = cadence if schedule
      end
    end

    it "returns nil when publish_schedule is nil" do
      provider = build_provider(nil, cadence: nil)

      provider.stub(:end_date, "2026-04-01") do
        _(provider.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_be_nil
      end
    end

    it "returns nil when end_date is nil" do
      provider = build_provider("*/30 14-16 * * 1-5")

      provider.stub(:end_date, nil) do
        _(provider.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_be_nil
      end
    end

    it "raises ArgumentError when publish_cadence is unrecognised" do
      bad = build_provider("*/30 14-16 * * 1-5", cadence: "biweekly")

      bad.stub(:end_date, "2026-04-01") do
        _ { bad.publishes_missed(reference_date: Date.new(2026, 4, 20)) }.must_raise(ArgumentError)
      end
    end

    describe "with daily cadence, Mon-Fri publishing (ECB-style)" do
      let(:mon_fri) { build_provider("*/30 14-16 * * 1-5") }

      it "returns 0 when end_date is Friday and reference is the following Monday" do
        # Fri 2026-04-17 to Mon 2026-04-20 exclusive: only Sat 18 and Sun 19 sit between — neither is a publish day.
        mon_fri.stub(:end_date, "2026-04-17") do
          _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(0)
        end
      end

      it "counts weekdays missed between end_date and reference" do
        # Last update Mon 2026-04-13, reference Fri 2026-04-17 exclusive: Tue 14, Wed 15, Thu 16 = 3.
        mon_fri.stub(:end_date, "2026-04-13") do
          _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 17))).must_equal(3)
        end
      end

      it "ignores weekends" do
        # Fri 2026-04-10 exclusive to Mon 2026-04-20 exclusive: Mon-Fri 13,14,15,16,17 = 5.
        mon_fri.stub(:end_date, "2026-04-10") do
          _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(5)
        end
      end
    end

    describe "with daily cadence, seven-day publishing" do
      let(:daily) { build_provider("*/30 14-16 * * *") }

      it "counts every day between end_date and reference" do
        # (2026-04-10, 2026-04-20) exclusive = 9 days.
        daily.stub(:end_date, "2026-04-10") do
          _(daily.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(9)
        end
      end
    end

    describe "with daily cadence, Mondays-only cron" do
      let(:mondays) { build_provider("*/30 21-23 * * 1") }

      it "counts only Mondays between end_date and reference" do
        # Mon 2026-04-06 exclusive to Fri 2026-04-24 exclusive: Mondays are 13, 20 = 2.
        mondays.stub(:end_date, "2026-04-06") do
          _(mondays.publishes_missed(reference_date: Date.new(2026, 4, 24))).must_equal(2)
        end
      end
    end

    describe "with weekly cadence (FRED-style, Monday publishing covers prior ISO week)" do
      let(:fred) { build_provider("*/30 21-23 * * 1", cadence: "weekly") }

      it "returns 0 when end_date is in the week FRED's last batch covered" do
        # Today 2026-04-21 (Tue). Last Monday fire Apr 20 covers prior ISO week Apr 13-19.
        # end_date = 2026-04-19 → same ISO week bucket as Apr 13 → 0 missed.
        fred.stub(:end_date, "2026-04-19") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 21))).must_equal(0)
        end
      end

      it "returns 0 when end_date's ISO week matches expected coverage bucket" do
        # end_date = Saturday 2026-04-18 (same ISO week as Apr 13-19). Still 0 missed.
        fred.stub(:end_date, "2026-04-18") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 21))).must_equal(0)
        end
      end

      it "returns 1 when end_date is one ISO week behind expected" do
        # end_date in week Apr 6-12; expected = week Apr 13-19 → 1 missed.
        fred.stub(:end_date, "2026-04-12") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 21))).must_equal(1)
        end
      end

      it "returns 0 on Sunday before Monday batch arrives (expected is 2 weeks ago)" do
        # Today Sun 2026-04-19. Last Mon fire Apr 13 covers prior week Apr 6-12.
        # end_date = Apr 12 (covered) → 0.
        fred.stub(:end_date, "2026-04-12") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 19))).must_equal(0)
        end
      end

      it "reports 1 missed on Monday morning when prior week's batch did not arrive" do
        # Today Mon 2026-04-20 (FRED's first publish day of this ISO week).
        # end_date Sun 2026-04-12 = end of ISO week Apr 6-12; expected = ISO week Apr 13-19 → 1 missed.
        fred.stub(:end_date, "2026-04-12") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(1)
        end
      end
    end

    describe "with monthly cadence (HKMA-style, day-of-month publishing covers prior month)" do
      let(:hkma) { build_provider("*/30 1-10 3-12 * *", cadence: "monthly") }

      it "returns 0 when current with March data and today is past April's window" do
        hkma.stub(:end_date, "2026-03-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(0)
        end
      end

      it "returns 0 when current with March data and today is before April's window" do
        hkma.stub(:end_date, "2026-03-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 2))).must_equal(0)
        end
      end

      it "returns 0 when last business day is not last calendar day (March 30 edge)" do
        # Nov 30 2025 was a Sunday; last HKMA date was Nov 29. Should still count as Nov covered.
        hkma.stub(:end_date, "2025-11-29") do
          _(hkma.publishes_missed(reference_date: Date.new(2025, 12, 15))).must_equal(0)
        end
      end

      it "returns 1 when April batch did not arrive and today is past May's window" do
        hkma.stub(:end_date, "2026-03-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 5, 15))).must_equal(1)
        end
      end

      it "returns 2 when two monthly batches are missed" do
        hkma.stub(:end_date, "2026-01-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(2)
        end
      end

      it "reports 1 missed on the first publish day of the window when prior month did not arrive" do
        # Today Apr 3 = first day of HKMA's publish window (DOM 3-12).
        # end_date Feb 28; expected = March → 1 missed.
        hkma.stub(:end_date, "2026-02-28") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 3))).must_equal(1)
        end
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
