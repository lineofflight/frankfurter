# frozen_string_literal: true

require_relative "../helper"
require "providers/base"

module Providers
  describe Base do
    let(:klass) do
      Class.new(Base) do
        class << self
          def key = "TEST"
          def name = "Test"
        end
      end
    end

    let(:provider) { klass.new }

    after do
      Providers.all.delete(klass)
    end

    it "requires fetch" do
      _ { provider.fetch }.must_raise(NotImplementedError)
    end

    describe "with a dataset" do
      let(:dataset) do
        [{ date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 1.1 }]
      end

      let(:provider) { klass.new(dataset:) }

      it "imports" do
        provider.import
        record = Rate.where(provider: "TEST").first

        _(record.base).must_equal("EUR")
        _(record.quote).must_equal("USD")
        _(record.rate).must_equal(1.1)
        _(record.provider).must_equal("TEST")
      end

      it "upserts" do
        2.times { provider.import }

        _(Rate.where(provider: "TEST").count).must_equal(1)
      end

      it "imports precious metal quotes" do
        dataset << { date: Date.today, provider: "TEST", base: "EUR", quote: "XAU", rate: 2000.0 }
        provider.import

        _(Rate.where(provider: "TEST", quote: "XAU").count).must_equal(1)
      end

      it "excludes unrecognised currency codes" do
        dataset << { date: Date.today, provider: "TEST", base: "EUR", quote: "SDR", rate: 1.5 }
        provider.import

        _(Rate.where(provider: "TEST", quote: "SDR").count).must_equal(0)
      end

      it "does not purge cache when no new rates are inserted" do
        provider.import
        cache_purged = false
        Cache.stub(:purge, -> { cache_purged = true }) do
          provider.import
        end

        _(cache_purged).must_equal(false)
      end

      it "flags outlier rates via consensus" do
        # Seed other providers with normal rates on today
        ["P1", "P2", "P3"].each do |p|
          Rate.unfiltered.insert(
            date: Date.today, provider: p, base: "EUR", quote: "USD", rate: 1.10,
          )
        end

        bad = [{ date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 999.0 }]
        klass.new(dataset: bad).import

        record = Rate.unfiltered.where(provider: "TEST", date: Date.today).first

        _(record[:outlier]).must_equal(true)
      end

      it "does not flag normal rates" do
        ["P1", "P2", "P3"].each do |p|
          Rate.unfiltered.insert(
            date: Date.today, provider: p, base: "EUR", quote: "USD", rate: 1.10,
          )
        end

        normal = [{ date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 1.12 }]
        klass.new(dataset: normal).import

        record = Rate.unfiltered.where(provider: "TEST", date: Date.today).first

        _(record[:outlier]).must_equal(false)
      end

      it "skips detection with fewer than 3 providers" do
        Rate.unfiltered.insert(
          date: Date.today, provider: "P1", base: "EUR", quote: "KES", rate: 1.10,
        )

        wild = [{ date: Date.today, provider: "TEST", base: "EUR", quote: "KES", rate: 999.0 }]
        klass.new(dataset: wild).import

        record = Rate.unfiltered.where(provider: "TEST", date: Date.today, quote: "KES").first

        _(record[:outlier]).must_equal(false)
      end
    end

    describe ".backfill" do
      let(:klass) do
        Class.new(Base) do
          class << self
            def key = "TEST"
            def name = "Test"
          end

          def fetch(since: nil, upto: nil)
            @dataset = [{ date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 1.1 }]
            self
          end
        end
      end

      it "imports via class method" do
        klass.backfill

        _(Rate.where(provider: "TEST").count).must_equal(1)
      end

      it "skips when already up to date" do
        Rate.dataset.insert(
          date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 1.1,
        )

        klass.stub(:new, -> { raise "should not be called" }) do
          klass.backfill
        end
      end

      it "chunks when range is given" do
        since = Date.today - 90
        Rate.dataset.insert(
          date: since, provider: "TEST", base: "EUR", quote: "USD", rate: 1.0,
        )

        fetches = []
        chunked_klass = Class.new(Base) do
          class << self
            def key = "TEST"
            def name = "Test"
          end

          define_method(:fetch) do |since: nil, upto: nil|
            fetches << { since:, upto: }
            @dataset = [{ date: since, provider: "TEST", base: "EUR", quote: "USD", rate: 1.1 }]
            self
          end
        end

        chunked_klass.backfill(range: 30)
        Providers.all.delete(chunked_klass)

        _(fetches.length).must_equal(4)
        _(fetches[0][:since]).must_equal(since)
        _(fetches[0][:upto]).must_equal(since + 29)
        _(fetches[-1][:upto]).must_be_nil
      end
    end
  end
end
