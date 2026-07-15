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
    # The USD/BOB rate held a stabilized peg (VENTA 6.96 / COMPRA 6.86, mid 6.91) from
    # 2011 until mid-2026, when Bolivia repriced the boliviano and the source replaced
    # its daily-sheet layout. The single official rate (TCO) is now published directly.
    #
    # Source:
    # 1. Yearly archives (USD/BOB only) at tiposDeCambioHistorico/xls.php?anio=YYYY.
    #    Used for historical data before 2008 (coverage starts 2000-01-01).
    # 2. Daily multi-currency spreadsheets at
    #    librerias/indicadores/otras/otras_imprimir2XLS.php?qdd=DD&qmm=MM&qaa=YYYY.
    #    Used for all currencies and precious metals from 2008-01-01 onwards.
    #
    # The daily sheet exists in two layouts; `parse_daily` detects and dispatches:
    #   - Legacy (through ~2026-06): currency marker in column 3, rate in column 4/5,
    #     USD split across USD.VENTA / USD.COMPRA rows averaged to a mid.
    #   - Current (2026-07 onward): ISO code in column 2, rate in column 3, USD carried
    #     as a single official rate (TCO), metals/SDR in their own labelled blocks.
    #
    # Rates are emitted in BCBO's native direction: foreign currency as base, BOB as
    # quote (1 USD = X BOB), matching NBG/BBK.
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

        rows = []
        sheet.each { |row| rows << row }

        # The current layout carries ISO codes in column 2; the legacy layout leaves
        # that column blank and puts its markers in column 3.
        if rows.any? { |row| row[2].to_s.strip.match?(/\A[A-Z]{3}\z/) }
          parse_daily_current(rows, date)
        else
          parse_daily_legacy(rows, date)
        end
      end

      private

      # Current layout (2026-07 onward). USD's official rate (Bs/USD) and every listed
      # currency (Bs per foreign unit) share one shape: ISO code in column 2, rate in
      # column 3. Metals and SDR sit in their own blocks with the value in column 3.
      def parse_daily_current(rows, date)
        records = []

        rows.each do |row|
          concept = row[0].to_s.strip
          moneda = row[1].to_s.strip
          code = row[2].to_s.strip

          if code.match?(/\A[A-Z]{3}\z/)
            rate = parse_rate(row[3])
            records << { date:, base: code, quote: "BOB", rate: } if rate&.positive?
          elsif moneda.match?(/DERECHO ESPECIAL DE GIRO/i)
            rate = parse_rate(row[3])
            records << { date:, base: "XDR", quote: "USD", rate: } if rate&.positive?
          else
            base = case concept
            when /\AORO\z/i then "XAU"
            when /\APLATA\z/i then "XAG"
            end
            next unless base

            rate = parse_rate(row[3])
            records << { date:, base:, quote: "USD", rate: } if rate&.positive?
          end
        end

        records
      end

      # Legacy layout (through ~2026-06). Currency marker in column 3, rate in column
      # 4/5, USD split across USD.VENTA / USD.COMPRA rows averaged to a mid.
      def parse_daily_legacy(rows, date)
        records = []
        usd_rates = []

        rows.each do |row|
          code_str = row[3].to_s.strip
          next if code_str.empty?

          if code_str == "USD./O.T.F."
            metal_name = row[0].to_s.strip
            base = case metal_name
            when /ORO/i then "XAU"
            when /PLATA/i then "XAG"
            end
            next unless base

            rate = parse_rate(row[4])
            next unless rate&.positive?

            records << { date:, base:, quote: "USD", rate: }
          elsif code_str == "USD/D.E.G."
            rate = parse_rate(row[5])
            next unless rate&.positive?

            records << { date:, base: "XDR", quote: "USD", rate: }
          elsif code_str == "USD.VENTA" || code_str == "USD.COMPRA"
            rate = parse_rate(row[4])
            usd_rates << rate if rate&.positive?
          elsif code_str == "USD"
            # Skip Ecuador's USD row to avoid duplicate USD/BOB rates
            next
          else
            next unless code_str.match?(/\A[A-Z]{3}\z/)

            rate = parse_rate(row[4])
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

      def parse_rate(cell)
        return cell.to_f if cell.is_a?(Numeric)

        Float(cell.to_s.delete(",").strip, exception: false)
      end

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
