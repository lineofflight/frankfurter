# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/hnb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe HNB do
      before do
        VCR.insert_cassette("hnb", match_requests_on: [:method, :host, :path])
      end

      after { VCR.eject_cassette }

      let(:adapter) { HNB.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2023, 1, 2), upto: Date.new(2023, 1, 6))

        _(dataset).wont_be_empty
        _(dataset.first[:base]).must_equal("EUR")
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2023, 1, 2), upto: Date.new(2023, 1, 6))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses JSON with comma decimals" do
        json = [
          {
            "datum_primjene" => "2023-01-02",
            "valuta" => "USD",
            "srednji_tecaj" => "1,066200",
          },
        ].to_json

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("EUR")
        _(records.first[:quote]).must_equal("USD")
        _(records.first[:rate]).must_be_close_to(1.0662, 0.0001)
        _(records.first[:date]).must_equal(Date.new(2023, 1, 2))
      end

      it "skips records with zero rates" do
        json = [
          {
            "datum_primjene" => "2023-01-02",
            "valuta" => "USD",
            "srednji_tecaj" => "0,000000",
          },
        ].to_json

        records = adapter.parse(json)

        _(records).must_be_empty
      end
    end
  end
end
