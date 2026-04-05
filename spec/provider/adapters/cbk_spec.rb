# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbk"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBK do
      before do
        VCR.insert_cassette("cbk", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { CBK.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 1))

        _(dataset).wont_be_empty
      end

      it "stores foreign currency as base and KES as quote" do
        records = adapter.parse({
          "data" => [
            ["20/03/2026", "US DOLLAR", "129.5012"],
          ],
        })

        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("KES")
        _(records.first[:rate]).must_equal(129.5012)
      end

      it "parses legacy table with mean rate" do
        records = adapter.parse(
          "data" => [
            ["15/06/2020", "EURO", "85.1234", "84.0000", "86.0000"],
          ],
        )

        _(records.first[:base]).must_equal("EUR")
        _(records.first[:quote]).must_equal("KES")
        _(records.first[:rate]).must_equal(85.1234)
      end

      it "resolves East African cross-rate names" do
        records = adapter.parse({
          "data" => [
            ["20/03/2026", "KES / USHS", "27.5000"],
            ["20/03/2026", "KES / TSHS", "20.1000"],
          ],
        })

        _(records.length).must_equal(2)
        _(records[0][:base]).must_equal("UGX")
        _(records[1][:base]).must_equal("TZS")
      end

      it "inverts East African cross rates" do
        records = adapter.parse({
          "data" => [
            ["20/03/2026", "KES / TSHS", "21.6800"],
            ["20/03/2026", "KES / USHS", "27.5000"],
            ["20/03/2026", "KEN SHILLING / RWF", "8.5000"],
          ],
        })

        _(records.length).must_equal(3)
        # 1 KES = 21.68 TZS means 1 TZS = 1/21.68 KES
        _(records[0][:base]).must_equal("TZS")
        _(records[0][:quote]).must_equal("KES")
        _(records[0][:rate]).must_be_close_to(1.0 / 21.68, 0.0001)

        _(records[1][:base]).must_equal("UGX")
        _(records[1][:quote]).must_equal("KES")
        _(records[1][:rate]).must_be_close_to(1.0 / 27.5, 0.0001)

        _(records[2][:base]).must_equal("RWF")
        _(records[2][:quote]).must_equal("KES")
        _(records[2][:rate]).must_be_close_to(1.0 / 8.5, 0.0001)
      end

      it "divides rates by unit marker parsed from currency name" do
        records = adapter.parse({
          "data" => [
            ["20/03/2026", "JPY (100)", "109.3764"],
          ],
        })

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("JPY")
        _(records.first[:quote]).must_equal("KES")
        _(records.first[:rate]).must_be_close_to(109.3764 / 100, 0.0001)
      end

      it "skips unmapped currencies" do
        records = adapter.parse({
          "data" => [
            ["20/03/2026", "UNKNOWN CURRENCY", "1.0000"],
            ["20/03/2026", "US DOLLAR", "129.5012"],
          ],
        })

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
      end
    end
  end
end
