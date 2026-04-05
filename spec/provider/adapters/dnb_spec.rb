# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/dnb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe DNB do
      before do
        VCR.insert_cassette("dnb", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { DNB.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2025, 3, 3), upto: Date.new(2025, 3, 7))

        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.length).must_be(:>=, 3)
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2025, 3, 3), upto: Date.new(2025, 3, 7))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses CSV with correct base and quote" do
        csv = <<~CSV
          VALUTA;KURTYP;TID;INDHOLD
          USD;KBH;2025M03D03;712.6900
        CSV

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("DKK")
        _(records.first[:rate]).must_be_close_to(7.1269, 0.0001)
        _(records.first[:date]).must_equal(Date.new(2025, 3, 3))
      end

      it "divides rate by 100 to normalize per-unit" do
        csv = <<~CSV
          VALUTA;KURTYP;TID;INDHOLD
          EUR;KBH;2025M03D03;745.8300
        CSV

        records = adapter.parse(csv)

        _(records.first[:rate]).must_be_close_to(7.4583, 0.0001)
      end

      it "skips missing values marked as .." do
        csv = <<~CSV
          VALUTA;KURTYP;TID;INDHOLD
          DEM;KBH;1977M01D03;..
        CSV

        records = adapter.parse(csv)

        _(records).must_be_empty
      end

      it "skips records with zero rate" do
        csv = <<~CSV
          VALUTA;KURTYP;TID;INDHOLD
          USD;KBH;2025M03D03;0.0000
        CSV

        records = adapter.parse(csv)

        _(records).must_be_empty
      end

      it "handles BOM in CSV" do
        csv = "\xEF\xBB\xBFVALUTA;KURTYP;TID;INDHOLD\nUSD;KBH;2025M03D03;712.6900\n"

        records = adapter.parse(csv)

        _(records.length).must_equal(1)
      end
    end
  end
end
