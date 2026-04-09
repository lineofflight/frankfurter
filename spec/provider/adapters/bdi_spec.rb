# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bdi"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BDI do
      before do
        VCR.insert_cassette("bdi", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BDI.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 9), upto: Date.new(2026, 2, 11))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 9), upto: Date.new(2026, 2, 11))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses CSV with correct base and quote" do
        csv = <<~CSV
          Country,Currency,ISO Code,UIC Code,Rate,Rate convention,Reference date (CET)
          UNITED STATES,Dollar,USD,001,1.1894,Foreign currency amount for 1 Euro.,2026-02-10
        CSV

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("EUR")
        _(records.first[:quote]).must_equal("USD")
        _(records.first[:rate]).must_equal(1.1894)
        _(records.first[:date]).must_equal(Date.new(2026, 2, 10))
      end

      it "skips N.A. rates" do
        csv = <<~CSV
          Country,Currency,ISO Code,UIC Code,Rate,Rate convention,Reference date (CET)
          BELARUS,Belarussian Ruble (new),BYN,280,N.A.,Foreign currency amount for 1 Euro.,2026-02-10
        CSV

        records = adapter.parse(csv)

        _(records).must_be_empty
      end

      it "skips zero rates" do
        csv = <<~CSV
          Country,Currency,ISO Code,UIC Code,Rate,Rate convention,Reference date (CET)
          UNITED STATES,Dollar,USD,001,0,Foreign currency amount for 1 Euro.,2026-02-10
        CSV

        records = adapter.parse(csv)

        _(records).must_be_empty
      end

      it "skips invalid currency codes" do
        csv = <<~CSV
          Country,Currency,ISO Code,UIC Code,Rate,Rate convention,Reference date (CET)
          INVALID,Currency,XX,999,1.5,Foreign currency amount for 1 Euro.,2026-02-10
        CSV

        records = adapter.parse(csv)

        _(records).must_be_empty
      end

      it "handles empty CSV" do
        csv = <<~CSV
          Country,Currency,ISO Code,UIC Code,Rate,Rate convention,Reference date (CET)
        CSV

        records = adapter.parse(csv)

        _(records).must_be_empty
      end
    end
  end
end
