# frozen_string_literal: true

require_relative "../helper"
require "providers/banxico"

module Providers
  describe BANXICO do
    let(:provider) { BANXICO.new }

    it "requires an API key" do
      _(BANXICO.api_key?).must_equal(true)
    end

    describe "with API key" do
      before do
        skip "BANXICO_API_KEY not set" unless ENV["BANXICO_API_KEY"]
        Rate.dataset.delete
        VCR.insert_cassette("banxico")
      end

      after { VCR.eject_cassette }

      def count_unique_dates
        Rate.select(:date).distinct.count
      end

      it "fetches rates since a date" do
        provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 15)).import

        _(count_unique_dates).must_be(:>, 1)
      end

      it "stores multiple currencies per date" do
        provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 15)).import
        date = Rate.first.date

        _(Rate.where(date:).count).must_be(:>, 1)
      end
    end

    describe "parse" do
      it "parses series response" do
        json = {
          bmx: {
            series: [
              {
                idSerie: "SF43718",
                datos: [
                  { fecha: "15/03/2026", dato: "17.1234" },
                  { fecha: "16/03/2026", dato: "17.2345" },
                ],
              },
              {
                idSerie: "SF46410",
                datos: [
                  { fecha: "15/03/2026", dato: "18.5678" },
                ],
              },
            ],
          },
        }.to_json

        rates = provider.parse(json)

        _(rates.length).must_equal(3)
        _(rates[0][:base]).must_equal("USD")
        _(rates[0][:quote]).must_equal("MXN")
        _(rates[0][:rate]).must_be_close_to(17.1234)
        _(rates[2][:base]).must_equal("EUR")
      end

      it "handles comma-formatted numbers" do
        json = {
          bmx: {
            series: [
              {
                idSerie: "SF43718",
                datos: [{ fecha: "15/03/2026", dato: "17,123.45" }],
              },
            ],
          },
        }.to_json

        rates = provider.parse(json)

        _(rates[0][:rate]).must_be_close_to(17_123.45)
      end

      it "skips invalid values" do
        json = {
          bmx: {
            series: [
              {
                idSerie: "SF43718",
                datos: [{ fecha: "15/03/2026", dato: "N/E" }],
              },
            ],
          },
        }.to_json

        rates = provider.parse(json)

        _(rates).must_be_empty
      end
    end
  end
end
