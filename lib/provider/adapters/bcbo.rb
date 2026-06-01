# frozen_string_literal: true

require "date"
require "net/http"
require "spreadsheet"
require "stringio"
require "uri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Bolivia — daily official rate ("tipo de cambio oficial")
    # of the boliviano against the US dollar, published on business days.
    #
    # The rate is stabilised: VENTA (sell) has sat at 6.96 and COMPRA (buy) at
    # 6.86 since 2011, so the mid (6.91) repeats day after day. That is the
    # value the bank publishes, so we relay it as-is.
    #
    # Source: one legacy OLE2/BIFF .xls per calendar year at
    # tiposDeCambioHistorico/xls.php?anio=YYYY. Each workbook holds a single
    # sheet "COTIZACIONES OFICIALES <year>" laid out as a matrix: column A is
    # the day of month (1..31), then twelve month blocks of two columns each
    # (VENTA, COMPRA) for ENERO..DICIEMBRE. Trailing rows ("PROM" averages and
    # the signature block) carry comma-decimal strings or text and are skipped
    # because only numeric day rows feed records. Requests for anio < 2000
    # silently fall back to the 2000 workbook, so 2000 is the earliest reliable
    # year.
    #
    # The bank publishes buy/sell; we average them to a mid (issue #314), same
    # as RBM and NRBT. Column header on the multi-currency table reads "TIPO DE
    # CAMBIO EN Bs POR UNIDAD DE MONEDA EXTRANJERA" — bolivianos per one foreign
    # unit — so foreign currency is the base and BOB the quote (pivot-in-quote),
    # matching NBG/BBK. 1 USD = 6.91 BOB.
    #
    # The bank also serves a daily multi-currency table (~50 currencies back to
    # ~2008) at librerias/indicadores/otras/otras_imprimir2XLS.php?qdd=DD&qmm=MM
    # &qaa=YYYY. Adding that basket is a follow-up; this adapter ships USD/BOB
    # from the yearly archive, which reaches back to 2000 in far fewer requests.
    class BCBO < Adapter
      HOST = "https://www.bcb.gob.bo"
      YEAR_URL = "#{HOST}/tiposDeCambioHistorico/xls.php".freeze
      EARLIEST_YEAR = 2000

      # Zero-based column of the VENTA (sell) cell for each month. COMPRA (buy)
      # sits in the next column. January starts at column 1 (column 0 is the
      # day), and each month occupies two columns.
      MONTH_SELL_COLUMNS = (1..12).to_h { |month| [month, (month - 1) * 2 + 1] }.freeze

      class << self
        # One workbook per calendar year. A wide window keeps fetch_each to a
        # handful of calls; fetch itself loops the calendar years in range and
        # sleeps between year downloads.
        def backfill_range = 3653
      end

      def fetch(after: nil, upto: nil)
        start_date = [after, Date.new(EARLIEST_YEAR, 1, 1)].compact.max
        end_date = upto || Date.today
        return [] if start_date > end_date

        dataset = []
        first = true
        (start_date.year..end_date.year).each do |year|
          sleep(0.5) unless first
          first = false

          xls = download(year)
          dataset.concat(parse(xls, year).select { |record| record[:date].between?(start_date, end_date) })
        end

        dataset
      end

      def parse(xls_data, year)
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

      private

      def download(year)
        uri = URI(YEAR_URL)
        uri.query = URI.encode_www_form(anio: year)

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
