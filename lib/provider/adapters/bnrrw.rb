# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banque Nationale du Rwanda (BNRRW). Publishes daily reference exchange
    # rates for 16 currencies against the Rwandan franc (RWF) via a public
    # JSON API at fxrates.bnr.rw. Rates are quoted as RWF per 1 unit of the
    # foreign currency — pivot RWF goes in quote, foreign in base.
    #
    # The endpoint serves one currency per request, so a full backfill iterates
    # the hard-coded currency list against chunked date windows. buying_rate /
    # average_rate / selling_rate are published; we take average_rate as the
    # mid per issue #314. Some historical values are quoted with thousands
    # commas (e.g. "1,253.60") while recent values are not — both are handled.
    #
    # Key BNR is taken by Banca Națională a României, so this provider is
    # keyed BNRRW.
    class BNRRW < Adapter
      BASE_URL = "https://fxrates.bnr.rw/currency_history/"

      # Quote currencies served by BNRRW. Verified live 2026-05-24 against the
      # currency_history endpoint. SDR returns no data so it's omitted.
      CURRENCIES = [
        "USD",
        "EUR",
        "GBP",
        "JPY",
        "CHF",
        "CAD",
        "AUD",
        "CNY",
        "INR",
        "ZAR",
        "AED",
        "SAR",
        "KES",
        "UGX",
        "TZS",
        "BIF",
      ].freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = after || (Date.today - 30)
        end_date = upto || Date.today

        records = []
        first = true
        CURRENCIES.each do |currency|
          sleep(0.3) unless first
          first = false

          records.concat(fetch_currency(currency, start_date, end_date))
        end

        records
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        return [] unless data.is_a?(Array)

        data.filter_map do |entry|
          code = entry["currency_name"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          rate_str = entry["average_rate"]
          next unless rate_str

          rate_value = Float(rate_str.to_s.delete(","))
          next unless rate_value.positive?

          date_str = entry["post_date"]
          next unless date_str

          date = Date.strptime(date_str, "%d-%b-%y")
          { date:, base: code, quote: "RWF", rate: rate_value }
        end
      end

      private

      def fetch_currency(currency, start_date, end_date)
        response = http.get(BASE_URL, params: {
          currency_name: currency,
          start_date: start_date.strftime("%Y-%m-%d"),
          end_date: end_date.strftime("%Y-%m-%d"),
        }).to_s

        parse(response)
      end
    end
  end
end
