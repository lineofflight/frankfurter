# frozen_string_literal: true

require "bigdecimal"
require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # European Commission monthly accounting rates: foreign units per EUR, or per ECU before January 1999. Each rate
    # applies from the first of its month. These period observations never blend with daily reference rates.
    class INFOREURO < Adapter
      API_URL = "https://ec.europa.eu/budg/inforeuro/api/public/monthly-rates"
      COVERAGE_START = Date.new(1994, 3, 1)
      # FRC is the source's Congolese-franc label from July 1998 through February 1999, before it adopts CDF.
      ALIASES = { "FRC" => "CDF", "ZIG" => "ZWG" }.freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = [after || COVERAGE_START, COVERAGE_START].max
        end_date = [upto || Date.today, Date.today].min
        cursor = Date.new(start_date.year, start_date.month, 1)
        cursor = cursor.next_month if cursor < start_date

        rows = []
        while cursor <= end_date
          body = http.get(API_URL, params: { year: cursor.year, month: cursor.month, lang: "en" }).to_s
          rows.concat(parse(body, date: cursor))
          cursor = cursor.next_month
          sleep(0.1) if cursor <= end_date
        end
        rows
      end

      def parse(body, date:)
        data = JSON.parse(body, decimal_class: BigDecimal)
        raise "INFOREURO: expected monthly rates" unless data.is_a?(Array) && data.any?

        base = date < Date.new(1999, 1, 1) ? "XEU" : "EUR"
        data.filter_map do |row|
          code = row.fetch("isoA3Code")
          quote = ALIASES.fetch(code, code)
          # Only January and February 2000 use AOK, at the new kwanza's magnitude (~5.5 per EUR), after the million:1
          # reform. March calls the same unit AOA; December 1999 still quotes AOR at 5,480,730 per EUR.
          quote = "AOA" if code == "AOK" && date >= Date.new(2000, 1, 1) && date < Date.new(2000, 3, 1)
          value = row.fetch("value")
          next if value.nil? || quote == base

          # Hyperinflation-era rates exceed the float-noise normalizer's 12 significant digits. Decimal input keeps
          # every published digit through ingestion, without a float conversion before storage.
          rate = BigDecimal(value.to_s)
          next unless rate.positive?

          { date:, base:, quote:, rate: }
        end
      end
    end
  end
end
