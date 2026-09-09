# frozen_string_literal: true

require "ox"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Palestine Monetary Authority. Palestine has no currency of its own; the PMA publishes daily buy, sell and mid
    # rates for the currencies circulating in the territories (ILS, JOD, USD) plus USD crosses for the majors, the Gulf
    # currencies, gold and silver. 25 pairs, Sunday to Thursday, archive from 2020-09-01.
    #
    # The public site embeds a small export tool at wcur.pma.ps. A GET on the export URL sets a PHP session cookie and
    # renders a form whose hidden anti-forgery input has a per-session name and value; a POST with that input, the
    # cookie and a from/to range returns an XLSX with Date, Pair, Buy, Sell, Mid columns. The whole archive fits in one
    # request (about 1 MB), so there is no chunking.
    #
    # Every cell in the workbook is a shared string, dates as "YYYY/MM/DD" and numbers with thousands separators
    # ("89,446.50") and the odd stray space. We take the published mid rather than synthesizing one from buy and sell.
    #
    # Direction is per pair, as labelled: "USD/ILS" is ILS per USD (USD in base), "GBP/USD" is USD per GBP (USD in
    # quote), and "JOD/ILS", "EUR/ILS", "EGP/ILS" do not touch the pivot at all. BaseConversion bridges the last three
    # through USD/ILS at query time.
    #
    # Terms (www.pma.ps/terms-of-use): content may be used for non-commercial purposes with the source acknowledged.
    # Attribution required.
    class PMA < Adapter
      EXPORT_URL = "https://wcur.pma.ps/ar/webtools/currency/export"
      ARCHIVE_START = Date.new(2020, 9, 1)

      TOKEN_PATTERN = /<input\s+type="hidden"\s+name="([^"]+)"\s+value="([^"]+)"/
      PAIR_PATTERN = %r{\A([A-Z]{3})/([A-Z]{3})\z}
      DATE_PATTERN = %r{\A(\d{4})[/-](\d{2})[/-](\d{2})\z}

      def fetch(after: nil, upto: nil)
        start_date = after || ARCHIVE_START
        end_date = upto || Date.today
        return [] if start_date > end_date

        parse(export(start_date, end_date))
      end

      def parse(xlsx)
        strings, sheet = read_workbook(xlsx)
        doc = Ox.load(sheet, mode: :generic, effort: :tolerant)
        rows = locate(doc, "sheetData")&.nodes || []

        rows.filter_map do |row|
          cells = row.nodes.to_h { |cell| [cell["r"].to_s.sub(/\d+\z/, ""), cell_value(cell, strings)] }
          parse_row(cells["A"], cells["B"], cells["E"])
        end
      end

      private

      def parse_row(date_text, pair_text, mid_text)
        date = parse_date(date_text)
        return unless date

        pair = pair_text.to_s.strip.match(PAIR_PATTERN)
        return unless pair

        rate = Float(mid_text.to_s.strip.delete(","), exception: false)
        return unless rate&.positive?

        { date:, base: pair[1], quote: pair[2], rate: }
      end

      def parse_date(text)
        match = text.to_s.strip.match(DATE_PATTERN)
        return unless match

        Date.new(match[1].to_i, match[2].to_i, match[3].to_i)
      rescue Date::Error
        nil
      end

      def export(start_date, end_date)
        form = http.get(EXPORT_URL)
        token = form.to_s.match(TOKEN_PATTERN)
        raise "PMA: no request token on #{EXPORT_URL}" unless token

        cookie = form.headers.get("Set-Cookie").map { |c| c.split(";").first }.join("; ")
        response = http
          .headers("Cookie" => cookie, "Referer" => EXPORT_URL)
          .post(EXPORT_URL, form: { token[1] => token[2], "from" => start_date.iso8601, "to" => end_date.iso8601 })

        unless response.mime_type.to_s.include?("spreadsheetml")
          raise "PMA: export returned #{response.mime_type.inspect} instead of a workbook"
        end

        response.to_s
      end

      def read_workbook(xlsx)
        sheet = nil
        strings_xml = nil

        Zip::File.open_buffer(xlsx) do |zip|
          sheet = zip.find_entry("xl/worksheets/sheet1.xml")&.get_input_stream&.read
          strings_xml = zip.find_entry("xl/sharedStrings.xml")&.get_input_stream&.read
        end
        raise "PMA: sheet1.xml missing from export workbook" unless sheet

        [shared_strings(strings_xml), sheet]
      end

      def shared_strings(xml)
        return [] unless xml

        doc = Ox.load(xml, mode: :generic, effort: :tolerant)
        root = doc.nodes.find { |n| n.is_a?(Ox::Element) }
        return [] unless root

        root.nodes.map { |si| si.locate("t").map { |t| t.nodes.first.to_s }.join }
      end

      def cell_value(cell, strings)
        value = cell.locate("v").first&.nodes&.first.to_s
        return strings[value.to_i] if cell["t"] == "s"
        return cell.locate("is/t").map { |t| t.nodes.first.to_s }.join if cell["t"] == "inlineStr"

        value
      end

      def locate(node, name)
        return node if node.is_a?(Ox::Element) && node.value == name
        return unless node.respond_to?(:nodes)

        node.nodes.each do |child|
          found = locate(child, name)
          return found if found
        end
        nil
      end
    end
  end
end
