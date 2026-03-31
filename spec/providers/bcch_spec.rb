# frozen_string_literal: true

require_relative "../helper"
require "providers/bcch"

module Providers
  describe BCCH do
    let(:provider) { BCCH.new }

    it "requires an API key" do
      _(BCCH.api_key?).must_equal(true)
    end

    describe "with API key" do
      before do
        skip "BCCH_USER not set" unless ENV["BCCH_USER"]
        Rate.dataset.delete
        VCR.insert_cassette("bcch")
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
          Codigo: 0,
          Descripcion: "Success",
          Series: {
            Obs: [
              { indexDateString: "10-03-2026", value: "916.36", statusCode: "OK" },
              { indexDateString: "11-03-2026", value: "893.69", statusCode: "OK" },
            ],
          },
        }.to_json

        rates = provider.parse(json, "USD")

        _(rates.length).must_equal(2)
        _(rates[0][:base]).must_equal("USD")
        _(rates[0][:quote]).must_equal("CLP")
        _(rates[0][:rate]).must_be_close_to(916.36)
      end

      it "skips NaN values" do
        json = {
          Codigo: 0,
          Series: {
            Obs: [
              { indexDateString: "07-03-2026", value: "NaN", statusCode: "ND" },
            ],
          },
        }.to_json

        rates = provider.parse(json, "USD")

        _(rates).must_be_empty
      end

      it "handles comma-formatted numbers" do
        json = {
          Codigo: 0,
          Series: {
            Obs: [
              { indexDateString: "10-03-2026", value: "1,916.36", statusCode: "OK" },
            ],
          },
        }.to_json

        rates = provider.parse(json, "USD")

        _(rates[0][:rate]).must_be_close_to(1916.36)
      end
    end
  end
end
