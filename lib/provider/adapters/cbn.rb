# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Nigeria. Publishes daily official exchange rates against
    # the Nigerian naira. One JSON request returns the full historical dataset
    # (2001-12-10 to present), which we fetch once and filter by date in memory.
    #
    # Rates use the centralrate field (mid of buy/sell). NGN is the pivot
    # currency, stored in the quote position; foreign currency is the base.
    #
    # Currency names arrive with whitespace variants and dual spellings (e.g.
    # "YEN"/"JAPANESE YEN", "POUND STERLING"/"POUNDS STERLING"). Names are
    # normalized via NAME_TO_ISO, which also maps SDR to its ISO 4217 code
    # XDR. WAUA (West African Unit of Account) is not ISO 4217 and is
    # therefore not mapped.
    #
    # Terms: https://www.cbn.gov.ng/Legal.html — redistribution permitted with
    # attribution, content may not be altered.
    class CBN < Adapter
      URL = "https://www.cbn.gov.ng/api/GetAllExchangeRates"

      # Normalized currency name (stripped + upcased) to ISO 4217 code.
      # CBN's "SDR" label maps to XDR (the ISO 4217 code for Special
      # Drawing Rights). WAUA is not ISO 4217 and is omitted.
      NAME_TO_ISO = {
        "CFA" => "XOF",
        "DANISH KRONA" => "DKK",
        "DANISH KRONER" => "DKK",
        "EURO" => "EUR",
        "JAPANESE YEN" => "JPY",
        "POUND STERLING" => "GBP",
        "POUNDS STERLING" => "GBP",
        "RIYAL" => "SAR",
        "SDR" => "XDR",
        "SOUTH AFRICAN RAND" => "ZAR",
        "SWISS FRANC" => "CHF",
        "UAE DIRHAM" => "AED",
        "US DOLLAR" => "USD",
        "YEN" => "JPY",
        "YUAN/RENMINBI" => "CNY",
      }.freeze

      def fetch(after: nil, upto: nil)
        response = Net::HTTP.get(URI(URL))
        records = parse(response)

        records = records.select { |r| r[:date] > after } if after
        records = records.select { |r| r[:date] <= upto } if upto
        records
      end

      def parse(json)
        data = Oj.load(json, mode: :strict)
        return [] unless data.is_a?(Array)

        data.filter_map do |entry|
          name = entry["currency"].to_s.strip.upcase
          iso = NAME_TO_ISO[name]
          next unless iso

          value = entry["centralrate"]
          next if value.nil? || value.to_s.strip.empty?

          rate = Float(value)
          next if rate.zero?

          date = Date.parse(entry["ratedate"])

          { date:, base: iso, quote: "NGN", rate: }
        end
      end
    end
  end
end
