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

      COLUMNS = ["Currency Code", "Currency Units per £1", "Start date"].freeze

      # Labels HMRC uses that aren't ISO 4217 codes: VED for the bolívar (VES), ZIG for Zimbabwe Gold (ZWG). The retired
      # sucre code ECS on Ecuador's row is left as is; validation drops it as unknown.
      ALIASES = {
        "VED" => "VES",
        "ZIG" => "ZWG",
      }.freeze

      # HMRC's November 2022 file labels an old-leone value (15631 per pound) as SLE. The new leone, ~24 per pound, only
      # appears in its files from March 2023, so earlier SLE rows price the old unit.
      PREDECESSORS = {
        "SLE" => ["SLL", Date.new(2023, 3, 1)],
      }.freeze

      class << self
        def backfill_range = 365

        # HMRC may issue a corrected rate mid-month when a currency moves more than 5%. Every file so far adds such a
        # row with its own start date, which parse keeps as a second observation; a replacement in place would only show
        # up as drift against the stored row.
        def revises? = true

        # Next month's file appears on the penultimate Thursday, so its rows sit up to two weeks ahead; a month covers
        # any slack.
        def lead_days = 31
      end

      def fetch(after: nil, upto: nil)
        start_date = after || COVERAGE_START
        # HMRC publishes the coming month's file on the penultimate Thursday of this one. Look one month past the window
        # so the rows dated the coming 1st are stored the day they appear rather than the first poll after they apply.
        end_date = upto || (Date.today >> 1)

        cursor = Date.new(start_date.year, start_date.month, 1)
        target_end = Date.new(end_date.year, end_date.month, 1)

        dataset = []
        while cursor <= target_end
          dataset.concat(fetch_month(cursor.year, cursor.month))
          cursor = cursor.next_month
        end

        dataset.select { |r| after.nil? || r[:date] >= after }
      end

      def parse(csv_data)
        # http.rb tags a text/csv body BINARY when the response carries no charset, and then the "£" in the column name
        # no longer matches the header. The file is UTF-8 either way.
        table = CSV.parse(csv_data.dup.force_encoding(Encoding::UTF_8), headers: true)
        missing = COLUMNS - table.headers
        raise "HMRC: CSV is missing columns #{missing.join(", ")}" unless missing.empty?

        rows = {}
        table.each do |row|
          raw_code = row["Currency Code"]&.strip
          next unless raw_code
          next unless row["Start date"] && row["Currency Units per £1"]

          date = Date.strptime(row["Start date"].strip, "%d/%m/%Y")
          code = historical_code(ALIASES.fetch(raw_code, raw_code), date)
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
        # Only the coming month's file may be missing, until HMRC publishes it. Any other 404 is a moved endpoint.
        raise unless e.response.code == 404 && Date.new(year, month, 1) > Date.today

        []
      end
    end
  end
end
