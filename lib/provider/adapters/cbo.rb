# frozen_string_literal: true

require "date"
require "nokogiri"
require "openssl"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Oman. Publishes daily buying and selling rates against the Omani rial (OMR) for 44 currencies,
    # plus gold, silver and platinum per troy ounce and the SDR, via a SharePoint WebForms search page. History reaches
    # back to 2017-10-15.
    #
    # The page's export button posts the form back with the ASP.NET VIEWSTATE and returns an HTML table dressed up as
    # ExchangeRates.xls. The "All" currency option ignores the date fields and returns only the latest snapshot, so a
    # date range has to be requested one currency at a time: a fetch is one GET for the tokens and currency list
    # followed by one POST per currency. Any range works in a single request (seven years came back in one go), so there
    # is no backfill_range and a full backfill is 49 requests: one GET plus 48 POSTs.
    #
    # Rates are OMR per unit of foreign currency (1 USD = 0.3845 OMR), so foreign goes in base and OMR in quote,
    # matching the pivot-in-quote convention of NBG and BBK. Buy and sell are coerced to a mid via midpoint. A date can
    # carry several intraday rows when the bank revised its fixing; the latest timestamp wins.
    #
    # TLS quirk: cbo.gov.om serves only its leaf certificate, so the default trust store can't build a chain to a root.
    # We bundle the DigiCert intermediate at config/cbo_ca_bundle.pem and pass it via an explicit ssl_context on each
    # request instead of disabling verification, as BOA does.
    class CBO < Adapter
      URL = "https://cbo.gov.om/Pages/DFESearch.aspx"
      CA_BUNDLE = File.expand_path("../../../config/cbo_ca_bundle.pem", __dir__)

      TOKENS = ["__VIEWSTATE", "__VIEWSTATEGENERATOR", "__EVENTVALIDATION"].freeze

      # Cell values arrive as SharePoint-typed strings, e.g. "string;#0.3845".
      VALUE_PREFIX = /\A[a-z]+;#/

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2017, 10, 15)
        end_date = upto || Date.today
        return [] if start_date > end_date

        page = http.get(URL, ssl_context:)
        cookies = extract_cookies(page)
        html = page.to_s
        tokens = extract_tokens(html)
        prefix = extract_prefix(html)
        codes = extract_currencies(html)

        records = codes.flat_map.with_index do |code, index|
          sleep(1) if index.positive?
          form = tokens.merge(
            "#{prefix}ddCurrencyCodes" => code,
            "#{prefix}dateFrom" => start_date.strftime("%d/%m/%Y"),
            "#{prefix}dateTo" => end_date.strftime("%d/%m/%Y"),
            "#{prefix}btnExport" => "",
          )
          parse(http.headers("Cookie" => cookies).post(URL, form:, ssl_context:).to_s)
        end

        records.select { |r| r[:date].between?(start_date, end_date) }
      end

      def parse(html)
        doc = Nokogiri::HTML.parse(html)
        header = doc.css("tr").find { |tr| tr.text.include?("Currency Code") }
        raise "CBO: no rates table in export" unless header

        latest = {}
        header.xpath("following-sibling::tr").each do |tr|
          cells = tr.css("td").map { |td| td.text.strip }
          next if cells.length < 6

          code = cells[0]
          published = DateTime.strptime(cells[1], "%d/%m/%Y %I:%M:%S %p")
          buy = number(cells[4])
          sell = number(cells[5])
          next unless buy&.positive? && sell&.positive?

          key = [published.to_date, code]
          next if latest[key] && latest[key][:published] > published

          latest[key] = { published:, rate: midpoint(buy, sell), **prices(bid: buy, ask: sell) }
        end

        latest.map do |(date, code), row|
          { date:, base: code, quote: "OMR", **row.except(:published) }
        end
      end

      private

      def ssl_context
        @ssl_context ||= OpenSSL::SSL::SSLContext.new.tap do |ctx|
          store = OpenSSL::X509::Store.new
          store.set_default_paths
          store.add_file(CA_BUNDLE)
          ctx.set_params(cert_store: store)
        end
      end

      def number(text)
        Float(text.sub(VALUE_PREFIX, ""), exception: false)
      end

      def extract_cookies(response)
        response.headers.get("Set-Cookie").map { |c| c.split(";").first }.join("; ")
      end

      def extract_tokens(html)
        TOKENS.to_h do |name|
          match = html.match(/name="#{name}"[^>]*value="([^"]*)"/)
          raise "CBO: #{name} not found on #{URL}" unless match

          [name, match[1]]
        end
      end

      # The web part's control prefix carries a SharePoint-assigned GUID, so read it off the page rather than pin it.
      def extract_prefix(html)
        html[/name="(ctl00\$[^"]*\$)ddCurrencyCodes"/, 1] || raise("CBO: currency dropdown not found on #{URL}")
      end

      def extract_currencies(html)
        doc = Nokogiri::HTML.parse(html)
        codes = doc.css("select.ddCurrencyCodes option").map { |o| o["value"] } - ["All"]
        raise "CBO: no currencies listed on #{URL}" if codes.empty?

        codes
      end
    end
  end
end
