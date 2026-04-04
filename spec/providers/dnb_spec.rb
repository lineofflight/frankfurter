# frozen_string_literal: true

require_relative "../helper"
require "providers/dnb"

module Providers
  describe DNB do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("dnb", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { DNB.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2025, 3, 3), upto: Date.new(2025, 3, 7)).import

      _(count_unique_dates).must_be(:>=, 3)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2025, 3, 3), upto: Date.new(2025, 3, 7)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses CSV with correct base and quote" do
      csv = <<~CSV
        VALUTA;KURTYP;TID;INDHOLD
        USD;KBH;2025M03D03;712.6900
      CSV

      records = provider.parse(csv)

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

      records = provider.parse(csv)

      _(records.first[:rate]).must_be_close_to(7.4583, 0.0001)
    end

    it "skips missing values marked as .." do
      csv = <<~CSV
        VALUTA;KURTYP;TID;INDHOLD
        DEM;KBH;1977M01D03;..
      CSV

      records = provider.parse(csv)

      _(records).must_be_empty
    end

    it "skips records with zero rate" do
      csv = <<~CSV
        VALUTA;KURTYP;TID;INDHOLD
        USD;KBH;2025M03D03;0.0000
      CSV

      records = provider.parse(csv)

      _(records).must_be_empty
    end

    it "handles BOM in CSV" do
      csv = "\xEF\xBB\xBFVALUTA;KURTYP;TID;INDHOLD\nUSD;KBH;2025M03D03;712.6900\n"

      records = provider.parse(csv)

      _(records.length).must_equal(1)
    end
  end
end
