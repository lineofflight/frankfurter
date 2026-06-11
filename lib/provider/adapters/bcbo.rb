# frozen_string_literal: true

require "date"
require "net/http"
require "spreadsheet"
require "stringio"
require "uri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Bolivia.
    #
    # Daily official exchange rates of the boliviano (BOB) against the US dollar and
    # ~50 other currencies, plus daily reference prices for gold, silver, and SDR.
    #
    # The USD/BOB rate is stabilized: VENTA (sell) has sat at 6.96 and COMPRA (buy) at
    # 6.86 since 2011, so the mid (6.91) repeats day after day.
    #
    # Source:
    # 1. Yearly archives (USD/BOB only) at tiposDeCambioHistorico/xls.php?anio=YYYY.
    #    Used for historical data before 2008 (coverage starts 2000-01-01).
    # 2. Daily multi-currency spreadsheets at
    #    librerias/indicadores/otras/otras_imprimir2XLS.php?qdd=DD&qmm=MM&qaa=YYYY.
    #    Used for all currencies and precious metals from 2008-01-01 onwards.
    #
    # Rates are emitted in BCBO's native direction: foreign currency as base, BOB as
    # quote (1 USD = 6.91 BOB), matching NBG/BBK.
    # Metals (XAU, XAG) and SDR (XDR) are quoted against USD in the daily sheets:
    #   - ORO (gold)   -> { base: "XAU", quote: "USD", rate: }
    #   - PLATA (silver) -> { base: "XAG", quote: "USD", rate: }
    #   - SDR (DEG)   -> { base: "XDR", quote: "USD", rate: }
    class BCBO < Adapter
      HOST = "https://www.bcb.gob.bo"
      YEAR_URL = "#{HOST}/tiposDeCambioHistorico/xls.php".freeze
      DAILY_URL = "#{HOST}/librerias/indicadores/otras/otras_imprimir2XLS.php".freeze
      EARLIEST_YEAR = 2000

      MONTH_SELL_COLUMNS = (1..12).to_h { |month| [month, (month - 1) * 2 + 1] }.freeze

      class << self
        # Daily queries are used from 2008 onwards, so keep the backfill range small
        # (30 days) to keep progress durable and avoid overloading the server.
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        start_date = [after, Date.new(EARLIEST_YEAR, 1, 1)].compact.max
        end_date = upto || Date.today
        return [] if start_date > end_date

        dataset = []

        # 1. Historical USD/BOB from yearly files (before 2008)
        pre_2008_end = [end_date, Date.new(2007, 12, 31)].min
        if start_date <= pre_2008_end
          first = true
          (start_date.year..pre_2008_end.year).each do |year|
            sleep(0.5) unless first
            first = false

            xls = download_year(year)
            dataset.concat(parse_yearly(xls, year).select { |r| r[:date].between?(start_date, pre_2008_end) })
          end
        end

        # 2. Multi-currency and metals from daily files (2008 onwards)
        post_2008_start = [start_date, Date.new(2008, 1, 1)].max
        if post_2008_start <= end_date
          first = true
          (post_2008_start..end_date).each do |date|
            next if date.saturday? || date.sunday?

            sleep(0.5) unless first
            first = false

            xls = download_daily(date)
            dataset.concat(parse_daily(xls, date))
          end
        end

        dataset
      end

      def parse_yearly(xls_data, year)
        book = Spreadsheet.open(StringIO.new(xls_data.to_s))
        sheet = book.worksheets.first
        return [] unless sheet

        records = []
        sheet.each do |row|
          day = row[0]
          next unless day.is_a?(Numeric)

          day = day.to_i
          next unless day.between?(1, 31)

          MONTH_SELL_COLUMNS.each do |month, sell_column|
            sell = row[sell_column]
            buy = row[sell_column + 1]
            next unless sell.is_a?(Numeric) && buy.is_a?(Numeric)
            next if sell.zero? || buy.zero?

            date = build_date(year, month, day)
            next unless date

            records << { date:, base: "USD", quote: "BOB", rate: (sell + buy) / 2.0 }
          end
        end

        records
      end

      def parse_daily(xls_data, date)
        book = Spreadsheet.open(StringIO.new(xls_data.to_s))
        sheet = book.worksheets.first
        return [] unless sheet

        records = []
        usd_rates = []

        sheet.each do |row|
          code_str = row[3].to_s.strip
          next if code_str.empty?

          if code_str == "USD./O.T.F."
            metal_name = row[0].to_s.strip
            base = case metal_name
            when /ORO/i then "XAU"
            when /PLATA/i then "XAG"
            end
            next unless base

            rate_val = row[4].to_s.delete(",")
            rate = Float(rate_val, exception: false)
            next unless rate&.positive?

            records << { date:, base:, quote: "USD", rate: }
          elsif code_str == "USD/D.E.G."
            rate_val = row[5].to_s.delete(",")
            rate = Float(rate_val, exception: false)
            next unless rate&.positive?

            records << { date:, base: "XDR", quote: "USD", rate: }
          elsif code_str == "USD.VENTA" || code_str == "USD.COMPRA"
            rate_val = row[4].to_s.delete(",")
            rate = Float(rate_val, exception: false)
            usd_rates << rate if rate&.positive?
          elsif code_str == "USD"
            # Skip Ecuador's USD row to avoid duplicate USD/BOB rates
            next
          else
            next unless code_str.match?(/\A[A-Z]{3}\z/)

            rate_val = row[4].to_s.delete(",")
            rate = Float(rate_val, exception: false)
            next unless rate&.positive?

            records << { date:, base: code_str, quote: "BOB", rate: }
          end
        end

        if usd_rates.size == 2
          mid_rate = usd_rates.sum / 2.0
          records << { date:, base: "USD", quote: "BOB", rate: mid_rate }
        end

        records
      end

      private

      def download_year(year)
        uri = URI(YEAR_URL)
        uri.query = URI.encode_www_form(anio: year)
        http_get(uri)
      end

      def download_daily(date)
        uri = URI(DAILY_URL)
        uri.query = URI.encode_www_form(qdd: date.day, qmm: date.month, qaa: date.year)
        http_get(uri)
      end

      def http_get(uri)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = (uri.scheme == "https")
        http.open_timeout = 30
        http.read_timeout = 120

        response = http.get(uri.request_uri)
        response.value
        response.body
      end

      def build_date(year, month, day)
        Date.new(year, month, day)
      rescue Date::Error
        nil
      end
    end
  end
end
