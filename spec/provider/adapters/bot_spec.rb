# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bot"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOT do
      before do
        ENV["BOT_API_KEY"] ||= "test"
        VCR.insert_cassette("bot", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BOT.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))

        _(dataset).wont_be_empty
      end

      it "returns multiple currencies" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))
        currencies = dataset.map { |r| r[:base] }.uniq

        _(currencies.size).must_be(:>, 10)
        _(currencies).must_include("USD")
        _(currencies).must_include("EUR")
      end

      it "quotes in THB" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))

        dataset.each { |r| _(r[:quote]).must_equal("THB") }
      end

      it "normalises per-unit rates" do
        body = {
          result: {
            data: {
              data_detail: [
                { period: "2026-04-03", currency_id: "JPY", currency_name_eng: "JAPAN : YEN (100 YEN) (JPY)", mid_rate: "20.4832000" },
                { period: "2026-04-03", currency_id: "IDR", currency_name_eng: "INDONESIA : RUPIAH (1,000 RUPIAH) (IDR)", mid_rate: "1.9296000" },
                { period: "2026-04-03", currency_id: "USD", currency_name_eng: "USA : DOLLAR (USD)", mid_rate: "32.6448000" },
              ],
            },
          },
        }.to_json

        records = adapter.parse(body)
        rates = records.to_h { |r| [r[:base], r[:rate]] }

        _(rates["JPY"]).must_be_close_to(0.204832, 0.0001)
        _(rates["IDR"]).must_be_close_to(0.0019296, 0.00001)
        _(rates["USD"]).must_be_close_to(32.6448, 0.01)
      end
    end
  end
end
