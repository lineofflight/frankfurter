# frozen_string_literal: true

require "csv"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of Serbia. Publishes the official middle rate of the dinar for about 25 currencies each working
    # morning, applied from 08:00 Belgrade time. Rates are RSD per unit of foreign currency, 1 or 100.
    #
    # DownloadPeriod returns a CSV of every currency over a range of up to about a year. It answers HTTP 500, not an
    # empty file, for a range that starts before the archive or ends after today, so fetch clamps both ends.
    #
    # Until September 2006 a list was formed on one day and applied the next. Rows are dated by application day, which
    # is also the day the endpoint filters on.
    #
    # Terms: https://www.nbs.rs/en/copyright/index.html
    class NBS < Adapter
      URL = "https://webappcenter.nbs.rs/ExchangeRateWebApp/ExchangeRate/DownloadPeriod"
      COVERAGE_START = Date.new(2002, 5, 15)
      MIDDLE_RATE = 3
      WORKING_DAYS = 1
      # Also carries each list forward over the weekends and holidays after it.
      ALL_DAYS = 2
      # The list still carries the euro's legacy currencies at their fixed conversion rates, all retired before the
      # archive starts.
      EXCLUDED_CURRENCIES = ["ATS", "BEF", "DEM", "ESP", "FIM", "FRF", "GRD", "IEP", "ITL", "LUF", "NLG", "PTE"].freeze

      class << self
        # 370 days returns 200, 400 returns 500.
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = [after ? after + 1 : COVERAGE_START, COVERAGE_START].max
        end_date = [upto || Date.today, Date.today].min
        return [] if start_date > end_date

        fetch_range(start_date, end_date)
      end

      # Columns: formation date, application date, numeric code, country, ISO code, unit, middle rate. The header row is
      # Cyrillic unless an English culture cookie is sent, so it is skipped by position.
      def parse(csv)
        rows(csv).filter_map do |_formed, applied, _numeric, _country, code, unit, value|
          next if EXCLUDED_CURRENCIES.include?(code)

          unit = Integer(unit)
          next if unit.zero?

          rate = Float(value) / unit
          next unless rate.positive?

          { date: Date.strptime(applied, "%d.%m.%Y"), base: code, quote: "RSD", rate: }
        end
      end

      private

      # A range that takes in a list applied on two days, such as the one formed on 2025-01-03 and applied again on
      # 2025-01-06, comes back with one arbitrary currency per day. Halve it until those days stand alone. A single day
      # comes back whole when asked for every calendar day, which only fills in days without a list of their own.
      def fetch_range(start_date, end_date)
        csv = download(start_date, end_date)
        return parse(csv) unless collapsed?(csv)

        if start_date == end_date
          csv = download(start_date, end_date, working_days: ALL_DAYS)
          raise "NBS: one currency in the list for #{start_date}" if collapsed?(csv)

          return parse(csv)
        end

        middle = start_date + ((end_date - start_date).to_i / 2)
        fetch_range(start_date, middle) + fetch_range(middle + 1, end_date)
      end

      def download(start_date, end_date, working_days: WORKING_DAYS)
        sleep(0.5)
        http.post(URL, form: {
          ExchangeRateListTypeID: MIDDLE_RATE,
          DateFrom: format_date(start_date),
          DateTo: format_date(end_date),
          WorkingDays: working_days,
          Format: "csv",
        },).to_s
      end

      def collapsed?(csv)
        rows = rows(csv)
        rows.any? && rows.uniq { |row| row[1] }.size == rows.size
      end

      def rows(csv)
        CSV.parse(csv).drop(1)
      end

      def format_date(date)
        date.strftime("%Y/%m/%d 12:00:00 AM")
      end
    end
  end
end
