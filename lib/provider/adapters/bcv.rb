# frozen_string_literal: true

require "date"
require "openssl"
require "spreadsheet"
require "stringio"
require "uri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Venezuela.
    #
    # Publishes the "Tipo de Cambio de Referencia" of the bolívar (VES) as one legacy .xls workbook per calendar
    # quarter, linked from a paginated statistics page. Each workbook holds one sheet per operation day, newest first,
    # named DDMMYYYY after the operation date. A sheet carries a "Fecha Operacion" and a "Fecha Valor" banner, a
    # two-line header, then one row per currency: ISO code, country, foreign-per-USD bid/ask, and Bs./M.E. bid/ask. The
    # Bs./M.E. ask is the figure BCV's homepage and press releases show as the reference rate; the bid is a fixed 0.25%
    # below it. We relay the ask.
    #
    # Workbook filenames follow 2_1_2{a-d}{YY}_smc.xls, a=Q1 through d=Q4, but the pattern can't be trusted on its own:
    # a re-uploaded quarter gets a Drupal suffix (2_1_2c23_smc_60.xls) while the unsuffixed name keeps serving a stale
    # two-sheet stub. We scrape the statistics page for the current link per quarter, walking its pages newest-first
    # until every quarter in the requested window has one.
    #
    # Rows are dated by Fecha Valor, the date the rate applies to, which is the next business day after the operation
    # date: Friday's sheet prices Monday. Workbooks group sheets by operation date, so a value date's sheet can sit in
    # the previous quarter's file; the fetch reaches back a couple of weeks to catch that at quarter boundaries.
    #
    # USD is the market-derived rate; every other Bs./M.E. figure is that rate crossed through the foreign-per-USD
    # column. Around 20 currencies are listed, including CUC (defunct, kept at USD parity) and Mexico's peso under the
    # pre-1993 code MXP, relabelled MXN here.
    #
    # The archive reaches back to 2020-03-30, but BCV redenominated the bolívar 1,000,000:1 on 2021-10-01 and every
    # sheet, before and after, prices "VES". The first post-redenomination value date is 2021-10-04, and the series
    # starts there so the two scales never share a code.
    #
    # TLS quirk: www.bcv.org.ve serves its leaf with a stale Sectigo intermediate that did not issue it, so the default
    # trust store can't build a chain to a root. We bundle the intermediate the leaf actually names at
    # config/bcv_ca_bundle.pem and pass it via an explicit ssl_context on each request rather than disabling
    # verification.
    #
    # Rates are emitted in BCV's native direction: foreign currency as base, VES as quote (1 USD = X VES).
    class BCV < Adapter
      DATA_URL = "https://www.bcv.org.ve/estadisticas/tipo-cambio-de-referencia-smc"
      CA_BUNDLE = File.expand_path("../../../config/bcv_ca_bundle.pem", __dir__)
      QUARTER_LETTERS = ["a", "b", "c", "d"].freeze
      WORKBOOK_LINK = /href=["']([^"']*2_1_2([a-d])(\d{2})_smc[^"']*\.xls)["']/i

      # First value date priced in bolívar digital, after the 2021-10-01 redenomination.
      FLOOR = Date.new(2021, 10, 4)

      # How far before the requested window to start fetching, in days, so the sheet that prices the window's first
      # value date is found even when it sits in the previous quarter's workbook.
      LOOKBACK = 14

      ALIASES = { "MXP" => "MXN" }.freeze

      def fetch(after: nil, upto: nil)
        start_date = [after, FLOOR].compact.max
        end_date = upto || Date.today
        return [] if start_date > end_date

        wanted = quarters(start_date - LOOKBACK, end_date)
        urls = workbook_urls(wanted)

        records = []
        wanted.each do |quarter|
          url = urls[quarter]
          # The current quarter's workbook appears on its first business day, so a missing link there is a not-yet.
          next if url.nil? && quarter == quarter_of(Date.today)
          raise "BCV: no workbook for #{quarter.join("Q")} on #{DATA_URL}" unless url

          sleep(0.5)
          records.concat(parse(download(url)))
        end

        records.select { |r| r[:date].between?(start_date, end_date) }
      end

      # Workbook links on one statistics page, keyed by [year, quarter]. The first link seen for a quarter wins.
      def workbook_links(html)
        html.scan(WORKBOOK_LINK).each_with_object({}) do |(path, letter, yy), links|
          quarter = [2000 + yy.to_i, QUARTER_LETTERS.index(letter.downcase) + 1]
          links[quarter] ||= URI.join(DATA_URL, path).to_s
        end
      end

      def parse(xls_data)
        book = Spreadsheet.open(StringIO.new(xls_data.to_s))
        book.worksheets.flat_map { |sheet| parse_sheet(sheet) }
      end

      private

      # Walk the statistics page, newest quarters first, until every wanted quarter has a link or a page adds none. An
      # out-of-range page comes back empty today; a pager that repeated its last page instead would add nothing new, so
      # either shape ends the walk.
      def workbook_urls(wanted)
        urls = {}
        (0..).each do |page|
          links = workbook_links(download(DATA_URL, page:))
          break if (links.keys - urls.keys).empty?

          urls = links.merge(urls)
          break if (wanted - urls.keys).empty?
        end
        urls
      end

      def parse_sheet(sheet)
        rows = sheet.map(&:to_a)
        date = value_date(rows) || raise("BCV: no Fecha Valor in sheet #{sheet.name}")
        ask = ask_column(rows) || raise("BCV: no Venta (ASK) column in sheet #{sheet.name}")

        rows.filter_map do |row|
          code = row[1]
          next unless code.is_a?(String) && code.match?(/\A[A-Z]{3}\z/)

          rate = row[ask]
          next unless rate.is_a?(Numeric) && rate.positive?

          { date:, base: ALIASES.fetch(code, code), quote: "VES", rate: rate.to_f }
        end
      end

      def value_date(rows)
        rows.each do |row|
          row.each do |cell|
            match = cell.to_s.match(%r{Fecha Valor:\s*(\d{2})/(\d{2})/(\d{4})})
            return Date.new(match[3].to_i, match[2].to_i, match[1].to_i) if match
          end
        end
        nil
      end

      # The header repeats "Venta (ASK)" for both column pairs; the Bs./M.E. one is the rightmost.
      def ask_column(rows)
        header = rows.find { |row| row.any? { |cell| cell.to_s.strip == "Venta (ASK)" } }
        header&.rindex { |cell| cell.to_s.strip == "Venta (ASK)" }
      end

      # Every [year, quarter] from the one holding start_date through the one holding end_date.
      def quarters(start_date, end_date)
        first = (start_date.year * 4) + quarter_of(start_date).last - 1
        last = (end_date.year * 4) + quarter_of(end_date).last - 1
        (first..last).map { |index| index.divmod(4).then { |year, offset| [year, offset + 1] } }
      end

      def quarter_of(date)
        [date.year, ((date.month - 1) / 3) + 1]
      end

      def download(url, params = {})
        http.get(url, params:, ssl_context:).to_s
      end

      # www.bcv.org.ve sends the wrong intermediate with its leaf, so the default trust store can't build a chain to a
      # root. We augment it with the Sectigo intermediate the leaf names instead of disabling verification.
      def ssl_context
        @ssl_context ||= OpenSSL::SSL::SSLContext.new.tap do |ctx|
          store = OpenSSL::X509::Store.new
          store.set_default_paths
          store.add_file(CA_BUNDLE)
          ctx.set_params(cert_store: store)
        end
      end
    end
  end
end
