# frozen_string_literal: true

require "date"
require "json"
require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Bosnia and Herzegovina. Published middle rates are BAM per foreign Units. Lists are generally
    # published Mon-Fri after 16:00 Sarajevo for the following day; retain their effective dates (usually Tue-Sat).
    class CBBH < Adapter
      PERIOD_URL = "https://www.cbbh.ba/CurrencyExchange/GetJsonForPeriod"
      DAILY_URL = "https://www.cbbh.ba/CurrencyExchange/GetJson"
      COVERAGE_START = Date.new(1998, 1, 6)

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = [after || COVERAGE_START, COVERAGE_START].max
        end_date = upto || Date.today
        return [] if start_date > end_date

        data = fetch_period(start_date, end_date)
        return confirm_empty(start_date, end_date) if data == "Problem with export"

        parse(data).select { |row| row[:date].between?(start_date, end_date) }
      end

      def parse(data)
        raise "CBBH: expected an array of exchange lists" unless data.is_a?(Array)

        data.flat_map do |list|
          unless list.is_a?(Hash) && list["CurrencyExchangeItems"].is_a?(Array)
            raise "CBBH: missing exchange-list items"
          end

          date = Date.parse(list.fetch("Date"))
          list["CurrencyExchangeItems"].filter_map do |item|
            code = item["AlphaCode"]
            next unless code&.match?(/\A[A-Z]{3}\z/)

            units = decimal(item["Units"])
            middle = decimal(item["Middle"])
            next unless units&.positive? && units.finite? && middle&.positive? && middle.finite?

            # Source Middle is not recomputed from Buy/Sell. Keep decimals through ingestion to preserve its digits.
            { date:, base: code, quote: "BAM", rate: middle / units }
          end
        end
      end

      private

      def fetch_period(start_date, end_date)
        response = http.use(ensure_success: { ignore: [404, 429] })
          .get(PERIOD_URL, params: { dateFrom: start_date.to_s, dateTo: end_date.to_s })
        body = response.to_s
        raise HTTP::StatusError, response if response.code == 404 && body.strip != '"Problem with export"'

        JSON.parse(body)
      end

      def decimal(value)
        BigDecimal(value.to_s.tr(",", "."), exception: false)
      end

      # The period endpoint uses the generic export-error string for genuinely empty Sun/Mon and holiday ranges too.
      # Only treat it as empty when the daily endpoint confirms its most recent actual list precedes our window.
      def confirm_empty(start_date, end_date)
        list = JSON.parse(http.get(DAILY_URL, params: { date: end_date.strftime("%m/%d/%Y 00:00:00") }).to_s)
        unless list.is_a?(Hash) && list["CurrencyExchangeItems"].is_a?(Array) && list["CurrencyExchangeItems"].any?
          raise "CBBH: could not confirm empty export"
        end
        return [] if Date.parse(list.fetch("Date")) < start_date

        raise "CBBH: period export failed despite an available list"
      end
    end
  end
end
