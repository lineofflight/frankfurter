# frozen_string_literal: true

require "date"
require "net/http"
require "openssl"
require "ox"
require "stringio"
require "uri"
require "zip"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Algeria — daily reference rate (cours moyen) against DZD for 17 quote
    # currencies. The full series is published as a single consolidated XLSX with one
    # sheet per quote currency. The archive is refreshed roughly once a month, so
    # rates typically lag by up to ~3 weeks. The latest day is also published as a
    # daily PDF (one per business day) — parsing the PDF is out of scope here.
    #
    # The XLSX URL embeds the publication year and month under /stoodroa/YYYY/MM/, so
    # the adapter scrapes the donnees-historiques hub for the current link rather than
    # hardcoding a path.
    #
    # TLS quirk: bank-of-algeria.dz serves only its leaf certificate. Ruby's net/http
    # rejects the chain because OpenSSL can't link the leaf to a trusted root. We
    # bundle the DigiCert intermediate at config/boa_ca_bundle.pem and load it into
    # the cert store at request time instead of disabling verification.
    #
    # Each sheet is named "<CCY> - DZD" (with "EURO" used in place of "EUR") and
    # contains two columns: Excel serial dates in column A, "1 CCY = X DZD" rates
    # in column B. We emit records with the foreign currency as base and DZD as
    # quote. JPY is published per 100, normalised here.
    #
    # MRO is the legacy Mauritanian ouguiya (replaced by MRU in 2018). The Money gem
    # already recognises it, so no patch is needed.
    #
    # Records are returned in BoA's native direction — foreign currency as base, DZD
    # as quote — matching the convention used by other pivot-in-quote adapters
    # (e.g. NBG, BBK).
    class BOA < Adapter
      HUB_URL = "https://www.bank-of-algeria.dz/donnees-historiques/"
      ARCHIVE_LINK = %r{href="(https://www\.bank-of-algeria\.dz/stoodroa/\d{4}/\d{2}/Cotation-DZD-[^"]+\.xlsx)"}
      CA_BUNDLE = File.expand_path("../../../config/boa_ca_bundle.pem", __dir__)
      EXCEL_EPOCH = Date.new(1899, 12, 30)
      JPY_UNITS = 100

      # The workbook sheet name "EURO - DZD" uses the legacy three-letter abbreviation
      # for the euro. Map it back to the ISO 4217 code.
      SHEET_NAME_OVERRIDES = {
        "EURO" => "EUR",
      }.freeze

      class << self
        # Whole-archive fetch — backfill rebuilds the full series each time the
        # consolidated XLSX is refreshed (roughly monthly). Large range keeps it
        # in a single fetch.
        def backfill_range = 36_525
      end

      def fetch(after: nil, upto: nil)
        xlsx_url = locate_archive_url
        xlsx_data = download(xlsx_url)
        parse(xlsx_data, after: after, upto: upto)
      end

      def parse(xlsx_data, after: nil, upto: nil)
        dataset = []

        Zip::File.open_buffer(StringIO.new(xlsx_data)) do |zip|
          workbook = Ox.parse(zip.read("xl/workbook.xml"))
          rels = parse_rels(zip.read("xl/_rels/workbook.xml.rels"))

          workbook.locate("*/sheets/sheet").each do |sheet_node|
            base = sheet_name_to_currency(sheet_node["name"])
            next unless base

            rid = sheet_node["r:id"] || sheet_node["id"]
            target = rels[rid]
            next unless target

            sheet_path = "xl/#{target}"
            sheet_xml = zip.read(sheet_path)
            dataset.concat(parse_sheet(sheet_xml, base, after: after, upto: upto))
          end
        end

        dataset
      end

      private

      def locate_archive_url
        body = http_get(URI(HUB_URL))
        match = body.match(ARCHIVE_LINK)
        raise "BoA: archive XLSX link not found on #{HUB_URL}" unless match

        match[1]
      end

      def download(url)
        http_get(URI(url))
      end

      def http_get(uri)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = (uri.scheme == "https")
        if http.use_ssl?
          store = OpenSSL::X509::Store.new
          store.set_default_paths
          store.add_file(CA_BUNDLE)
          http.cert_store = store
        end
        http.open_timeout = 30
        http.read_timeout = 120

        response = http.get(uri.request_uri)
        response.value
        response.body
      end

      def parse_rels(xml)
        Ox.parse(xml).locate("*/Relationship").to_h do |rel|
          [rel["Id"], rel["Target"]]
        end
      end

      def sheet_name_to_currency(name)
        return unless name

        code = name.split(%r{\s*[-/]\s*}, 2).first.to_s.strip.upcase
        code = SHEET_NAME_OVERRIDES.fetch(code, code)
        return unless code.match?(/\A[A-Z]{3}\z/)

        code
      end

      def parse_sheet(xml, base, after:, upto:)
        doc = Ox.parse(xml)
        records = []

        doc.locate("*/sheetData/row").each do |row|
          serial = nil
          value = nil

          row.locate("c").each do |cell|
            ref = cell["r"]
            next unless ref

            col = ref.match(/\A([A-Z]+)/)[1]
            cell_type = cell["t"]
            next if cell_type == "s" # shared-string header cell

            v_node = cell.locate("v").first
            next unless v_node

            text = v_node.text
            next if text.nil? || text.empty?

            case col
            when "A"
              serial = Integer(text, exception: false) || Float(text, exception: false)&.to_i
            when "B"
              value = Float(text, exception: false)
            end
          end

          next unless serial && value

          date = EXCEL_EPOCH + serial
          next if after && date < after
          next if upto && date > upto

          rate = base == "JPY" ? value / JPY_UNITS : value
          next if rate.zero?

          records << { date: date, base: base, quote: "DZD", rate: rate }
        end

        records
      end
    end
  end
end
