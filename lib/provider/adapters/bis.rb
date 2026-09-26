# frozen_string_literal: true

require "csv"
require "date"
require "provider/adapters/adapter"

class Provider
  module Adapters
    # BIS WS_XRU monthly end-of-period USD exchange rates. These are distinct from its monthly averages and daily
    # series. Historical observations can be restated in successor units; never reconstruct predecessor magnitudes.
    # https://www.bis.org/statistics/xrusd/xrusd_doc.pdf documents the national sources and historical adjustments.
    class BIS < Adapter
      BASE_URL = "https://stats.bis.org/api/v2/data/dataflow/BIS/WS_XRU/1.0/M...E"
      COVERAGE_START = Date.new(1900, 1, 1)
      COLUMNS = ["FREQ", "REF_AREA", "CURRENCY", "COLLECTION", "TIME_PERIOD", "OBS_VALUE", "UNIT_MULT"].freeze

      # National EUR series are synthetic legacy-currency histories and differ before euro adoption. XM is the actual
      # euro-area/ECU series. The currency unions also have differing national histories (especially Guinea-Bissau's XOF
      # series). Select a fixed representative area, not whichever row happens to arrive first.
      AREAS = { "EUR" => "XM", "AUD" => "AU", "XOF" => "WA", "XAF" => "CM", "XCD" => "AG" }.freeze

      class << self
        def backfill_range = 3650
        def revises? = true

        # Major currencies arrive ahead of many IMF-sourced series. Provider resumes from the newest observation, so
        # revisit a year to catch late monthly rows and flag recent revisions. Older revisions need a manual backfill.
        def fetch_each(after: nil, &)
          return if after && after >= Date.today

          super(after: after && [after << 12, COVERAGE_START].max, &)
        end
      end

      def fetch(after: nil, upto: nil)
        start_date = after || COVERAGE_START
        end_date = upto || Date.today
        return [] if start_date > end_date

        response = http.headers("Accept" => "application/vnd.sdmx.data+csv;version=1.0.0").get(BASE_URL, params: {
          "c[TIME_PERIOD]" => "ge:#{start_date.strftime("%Y-%m")}+le:#{end_date.strftime("%Y-%m")}",
        },)
        parse(response.to_s).select { |r| r[:date].between?(start_date, end_date) }
      end

      def parse(csv)
        table = CSV.parse(csv, headers: true)
        missing = COLUMNS - table.headers
        raise "BIS: CSV is missing columns #{missing.join(", ")}" unless missing.empty?

        table.filter_map do |row|
          next unless row["FREQ"] == "M" && row["COLLECTION"] == "E"

          code = row["CURRENCY"]
          next if code == "USD" || (AREAS.key?(code) && row["REF_AREA"] != AREAS[code])

          value = Float(row["OBS_VALUE"], exception: false)
          next unless value&.positive? && value.finite?

          month = Date.strptime(row["TIME_PERIOD"], "%Y-%m")
          date = Date.new(month.year, month.month, -1)
          rate = (BigDecimal(row["OBS_VALUE"]) * (10**Integer(row["UNIT_MULT"]))).to_f
          { date:, base: "USD", quote: currency(code, date), rate: }
        end
      end

      private

      def currency(code, date)
        # SLL is restated throughout in new-leone units (2017-12: 7.53696; 2022-06: 13.15315; 2022-07: 13.88).
        return "SLE" if code == "SLL"
        # VEF stops in 2018-07 at 172368, then resumes in 2019-06 at 6550.047641 in soberano units. The 2021
        # redenomination retains VES, matching BCV's own series. MRO and STD remain in genuine predecessor units even
        # after retirement (2024-08 MRO: 396; 2026-06 STD: 21479.9), so their labels and values are preserved.
        return "VES" if code == "VEF" && date >= Date.new(2019, 6, 1)
        return "XEU" if code == "EUR" && date < Date.new(1999, 1, 1)

        code
      end
    end
  end
end
