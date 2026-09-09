# frozen_string_literal: true

require "bigdecimal"
require "cgi"
require "date"
require "pdf-reader"
require "stringio"
require "uri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco de Mocambique — daily reference rates against MZN for 19 currencies, published as one PDF bulletin (~20 KB)
    # per business day. USD/MZN is the bank's own 15:30 fixing from commercial-bank reporting; every other row is that
    # fixing crossed with the Reuters USD rate for the currency (issue #386).
    #
    # There is no rate API. The Mercado Cambial page links one listing page per multi-year period
    # (/pt/tabelas-de-taxas-de-cambio-de-referencia-diarias/2026-2025/ and so on back to 2018), and each listing links
    # every bulletin in its period under an opaque media slug. We read the period slugs off the index, fetch only the
    # listings whose years overlap the requested window, collect (date, pdf_url) pairs from the DDMMYYYY suffix of each
    # filename, then fetch and parse each PDF.
    #
    # The bulletin is a fixed-layout text table: country, currency, buy (COMPRA), sell (VENDA) and, since November 2020,
    # a published mid (MÉDIA). Rows are keyed on the country label because the currency label is unreliable: "Coroa" and
    # "Kwacha" each appear more than once, and the USD row sometimes drops its label altogether. The published mid is
    # emitted where present; older bulletins carry only buy and sell, so the mid is synthesised with `midpoint`. Section
    # headings state the unit ("Meticais por Unidade" or "por 1000 Unidades"), and the per-1000 block (JPY, MWK, TZS) is
    # rescaled to per-unit.
    #
    # The Zimbabwe row is dropped: it has carried the same figure (about 167 MZN per 1000) since 2019, matching neither
    # ZWL nor its 2024 successor ZWG, so no ISO code can be assigned honestly.
    #
    # Direction: foreign currency in base, MZN in quote (1 foreign = X MZN), matching the convention used by other
    # pivot-in-quote adapters (e.g. NBG, BBK).
    class BM < Adapter
      HOST = "https://www.bancomoc.mz"
      INDEX_URL = "#{HOST}/pt/areas-de-actuacao/mercados/mercado-cambial/".freeze
      LISTING_HREF = %r{href="(/pt/tabelas-de-taxas-de-cambio-de-referencia-diarias/(\d{4})-(\d{4})/)"}
      PDF_HREF = %r{href="(/media/[^"/]+/[^"]*?(\d{2})(\d{2})(\d{4})\.pdf)"}

      # Section headings announce how many units of the foreign currency the row prices.
      UNITS_PATTERN = /Meticais por (?:(\d+) )?Unidade/i
      # Country and currency labels, then buy, sell and (optionally) mid. Labels start with a letter so numbered section
      # headings never match.
      ROW_PATTERN = /\A(\D.*?)\s+([\d.,]+)\s+([\d.,]+)(?:\s+([\d.,]+))?\z/
      # Rates live in sections 1 and 2; section 3 carries prime rate, SOFR and gold, which must not be read as rows.
      END_OF_RATES = /\A3\.\s+OUTRAS INFORMA/

      # Maps the country label in the bulletin to the ISO 4217 code of its currency. Older bulletins call eSwatini by
      # its former name.
      COUNTRIES = {
        "Estados Unidos" => "USD",
        "Àfrica do Sul" => "ZAR",
        "Botswana" => "BWP",
        "eSwatini" => "SZL",
        "Swazilândia" => "SZL",
        "Mauricias" => "MUR",
        "Zâmbia" => "ZMW",
        "Japão" => "JPY",
        "Malawi" => "MWK",
        "Tanzânia" => "TZS",
        "Brasil" => "BRL",
        "Canada" => "CAD",
        "China/Offshore" => "CNH",
        "China" => "CNY",
        "Dinamarca" => "DKK",
        "Inglaterra" => "GBP",
        "Noruega" => "NOK",
        "Suécia" => "SEK",
        "Suíça" => "CHF",
        "União Europeia" => "EUR",
      }.freeze

      class << self
        # Chunk the archive so partial progress survives an unparseable PDF — fetch_each upserts after every window.
        # discover_pdfs re-reads the index and the (large) listing page per chunk, so keep chunks wide.
        def backfill_range = 60
      end

      def fetch(after: nil, upto: nil)
        upto ||= Date.today
        entries = discover_pdfs(after, upto)

        dataset = []
        entries.each_with_index do |(date, url), index|
          sleep(0.5) if index.positive?

          dataset.concat(parse(extract_text(http.get(url).to_s), date))
        end

        dataset
      end

      def parse(text, date)
        units = 1
        records = []

        text.each_line do |raw|
          line = raw.strip
          break if line.match?(END_OF_RATES)

          if (heading = line.match(UNITS_PATTERN))
            units = heading[1] ? Integer(heading[1], 10) : 1
            next
          end

          row = line.match(ROW_PATTERN)
          next unless row

          code = COUNTRIES[country(row[1])]
          next unless code

          buy = row[2]
          sell = row[3]
          mid = row[4]
          rate = mid ? number(mid) : midpoint(number(buy), number(sell))
          rate /= units if units > 1
          next if rate.zero?

          records << { date: date, base: code, quote: "MZN", rate: rate }
        end

        records
      end

      private

      def extract_text(pdf_data)
        PDF::Reader.new(StringIO.new(pdf_data)).pages.map(&:text).join("\n")
      end

      # The label cell holds the country, a footnote marker like "(a)" on some rows, then the currency name after a run
      # of spaces. Only the country identifies the row.
      def country(label)
        label.sub(/\(\w\)/, "").strip.split(/\s{2,}/).first
      end

      # Decimal comma; a thousands dot never appears in a rate row but is harmless to drop.
      def number(text)
        Float(text.delete(".").tr(",", "."))
      end

      def discover_pdfs(after, upto)
        entries = {}

        listings(after, upto).each_with_index do |url, index|
          sleep(0.5) if index.positive?

          fetch_listing(url).each do |date, pdf_url|
            next if after && date < after
            next if date > upto

            entries[date] ||= pdf_url
          end
        end

        entries.sort.to_a
      end

      # Listing pages are named for the years they span ("2026-2025", "2024-2022"). The newest is treated as open-ended
      # so a window reaching into a year the bank has not yet split off still finds the current listing.
      def listings(after, upto)
        index = http.get(INDEX_URL).to_s
        periods = index.scan(LISTING_HREF).map do |href, first, second|
          years = [Integer(first, 10), Integer(second, 10)]
          [years.min, years.max, "#{HOST}#{href}"]
        end
        raise "no listing pages on #{INDEX_URL}" if periods.empty?

        periods = periods.uniq(&:last).sort_by(&:first)
        periods.last[1] = Float::INFINITY

        periods.filter_map do |first, last, url|
          next if after && after.year > last
          next if upto.year < first

          url
        end
      end

      def fetch_listing(url)
        body = http.get(url).to_s
        body.force_encoding(Encoding::UTF_8) if body.encoding != Encoding::UTF_8

        body.scan(PDF_HREF).map do |href, day, month, year|
          escaped_path = URI::RFC2396_PARSER.escape(CGI.unescapeHTML(href))
          [Date.new(Integer(year, 10), Integer(month, 10), Integer(day, 10)), URI.join(HOST, escaped_path).to_s]
        end
      end
    end
  end
end
