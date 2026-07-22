# frozen_string_literal: true

require "date"
require "pdf-reader"
require "stringio"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banque de la Republique du Burundi — daily reference rates against BIF for 19
    # currencies. Published as a single PDF bulletin per business day (~220 KB),
    # listed on a paginated Drupal index at /en/affichagetoustauxchange. Each PDF
    # carries Acheteur (buy), Cours moyen jour (mid), and Vendeur (sell); we keep
    # the mid (issue #314).
    #
    # The site exposes only a paginated index — no date-range API. We walk the
    # index, collect (date, pdf_url) pairs within the requested window, fetch each
    # PDF, and parse the fixed-layout table with pdf-reader.
    #
    # Currency labels are French names. The mapping table covers all 19 quote
    # currencies served by the bulletin. DTS is BRB's label for Special Drawing
    # Rights and is emitted as XDR per ISO 4217.
    #
    # Eleven currencies carry an asterisk meaning "Monnaies non admises au change
    # manuel" (not accepted by manual exchange bureaus). It's informational only;
    # the rate is authoritative, so the asterisk is stripped from the label.
    #
    # Direction: foreign currency in base, BIF in quote (1 foreign = X BIF), matching
    # the convention used by other pivot-in-quote adapters (e.g. NBG, BBK).
    class BRB < Adapter
      HOST = "https://www.brb.bi"
      INDEX_URL = "#{HOST}/en/affichagetoustauxchange".freeze
      PDF_HREF = %r{href="(/sites/default/files/\d{4}-\d{2}/Cours%20de%20change%20du%20(\d{2})-(\d{2})-(\d{4})[^"]*\.pdf)"}
      ROW_PATTERN = /\A1\s+(.+?)\*?\s+([\d.,]+)\s+([\d.,]+)\s+([\d.,]+)\s*\z/

      # Maps the French currency labels in the BRB bulletin to ISO 4217 codes.
      # DTS (Droits de Tirage Speciaux) maps to XDR.
      CURRENCY_NAMES = {
        "Dollar Canadien" => "CAD",
        "Couronne Danoise" => "DKK",
        "Yen Japonais" => "JPY",
        "Couronne Norvegienne" => "NOK",
        "Livre Sterling" => "GBP",
        "Couronne Suedoise" => "SEK",
        "Dollar USA" => "USD",
        "Franc Suisse" => "CHF",
        "Euro" => "EUR",
        "Shilling Kenyan" => "KES",
        "DTS" => "XDR",
        "Rand Sud-Africain" => "ZAR",
        "Dollar Australien" => "AUD",
        "Shilling Tanzanien" => "TZS",
        "Shilling Ougandais" => "UGX",
        "Franc Rwandais" => "RWF",
        "Yuan Renmimbi" => "CNY",
        "Dinar Kowetien" => "KWD",
        "Riyal Saoudien" => "SAR",
      }.freeze

      class << self
        # Chunk the archive so partial progress survives an unparseable PDF — fetch_each
        # upserts after every window. discover_pdfs re-walks the (small) index per chunk.
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        upto ||= Date.today
        entries = discover_pdfs(after, upto)

        dataset = []
        first = true
        entries.each do |date, url|
          sleep(0.5) unless first
          first = false

          pdf_data = http.get(url).to_s
          next unless pdf_data.start_with?("%PDF")

          dataset.concat(parse(pdf_data, date))
        end

        dataset
      end

      def parse(pdf_data, date)
        reader = PDF::Reader.new(StringIO.new(pdf_data))
        text = reader.pages.map(&:text).join("\n")

        text.each_line.filter_map do |line|
          match = line.strip.match(ROW_PATTERN)
          next unless match

          label = match[1].strip
          code = CURRENCY_NAMES[label]
          next unless code

          rate = Float(match[3].tr(",", "."))
          next if rate.zero?

          { date: date, base: code, quote: "BIF", rate: rate }
        end
      end

      private

      def discover_pdfs(after, upto)
        entries = {}
        page = 0
        first = true

        loop do
          sleep(0.5) unless first
          first = false

          page_entries = fetch_index_page(page)
          break if page_entries.empty?

          page_entries.each do |date, url|
            next if after && date < after
            next if date > upto

            entries[date] ||= url
          end

          oldest = page_entries.map(&:first).min
          break if after && oldest < after

          page += 1
        end

        entries.sort.to_a
      end

      def fetch_index_page(page)
        params = { page: page } if page.positive?

        body = http.get(INDEX_URL, params: params).to_s
        body.force_encoding(Encoding::UTF_8) if body.encoding != Encoding::UTF_8

        body.scan(PDF_HREF).map do |href, day, month, year|
          [Date.new(Integer(year, 10), Integer(month, 10), Integer(day, 10)), "#{HOST}#{href}"]
        end.uniq { |date, _| date }
      end
    end
  end
end
