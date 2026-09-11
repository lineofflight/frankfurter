# frozen_string_literal: true

require "csv"
require "date"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # HM Revenue & Customs (UK). Publishes monthly customs exchange rates for 150+ currencies against the British pound
    # (GBP). Rates are published on the penultimate Thursday of every month and take effect on the 1st of the following
    # month. Frequency monthly, so these never blend (#172, #612, #646).
    class HMRC < Adapter
      BASE_URL = "https://www.trade-tariff.service.gov.uk/uk/api/exchange_rates/files"
      COVERAGE_START = Date.new(2021, 1, 1)

      # Non-standard or temporary codes used by HMRC mapped to their ISO 4217 equivalents. ECS was used for Ecuador
      # (which uses USD), VED is Venezuelan Bolívar (VES), and ZIG was Zimbabwe Gold (ZWG).
      ALIASES = {
        "ECS" => "USD",
        "VED" => "VES",
        "ZIG" => "ZWG",
      }.freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = after || COVERAGE_START
        end_date = upto || Date.today
        return [] if start_date > end_date

        cursor = Date.new(start_date.year, start_date.month, 1)
        target_end = Date.new(end_date.year, end_date.month, 1)

        dataset = []
        while cursor <= target_end
          dataset.concat(fetch_month(cursor.year, cursor.month))
          cursor = cursor.next_month
        end

        dataset.select { |r| (after.nil? || r[:date] >= after) && (upto.nil? || r[:date] <= upto) }
      end

      def parse(csv_data)
        rows = {}
        CSV.parse(csv_data, headers: true).each do |row|
          raw_code = row["Currency Code"]&.strip
          next unless raw_code
          next unless row["Start date"] && row["Currency Units per £1"]

          code = ALIASES.fetch(raw_code, raw_code)
          date = Date.strptime(row["Start date"].strip, "%d/%m/%Y")
          rate = Float(row["Currency Units per £1"], exception: false)
          next unless rate&.positive?

          rows[[date, code]] ||= { date:, base: "GBP", quote: code, rate: }
        end
        rows.values
      end

      private

      def fetch_month(year, month)
        url = "#{BASE_URL}/monthly_csv_#{year}-#{month}.csv"
        parse(http.get(url).to_s)
      rescue HTTP::StatusError => e
        raise unless e.response.code == 404

        []
      end
    end
  end
end
