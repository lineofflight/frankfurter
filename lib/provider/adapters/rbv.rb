# frozen_string_literal: true

require "date"
require "nokogiri"
require "openssl"

require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    # Reserve Bank of Vanuatu. Publishes daily reference rates against the
    # Vanuatu vatu (VUV) on business days, 08:30-09:00 Pacific/Efate. Six quote
    # currencies (USD, JPY, NZD, GBP, AUD, EUR), the VUV trade-weighted basket.
    #
    # The exchange-rates page is a Joomla Fabrik list. A CSV export endpoint
    # exists but is hard-capped at 100 rows per call, so we scrape the HTML list
    # directly with a `limit1` query param large enough to return every row in
    # one response. Rows render with the date in either "DD Month YYYY" or
    # "DD-Mon-YY" form depending on age; Date.parse handles both.
    #
    # Rates are VUV per 1 unit of foreign currency. JPY is published per single
    # unit (not per 100), so no normalization is needed.
    #
    # TLS quirk: www.rbv.gov.vu serves a malformed chain that omits its issuer,
    # the Trustico RSA DV SSL CA 2 intermediate. Ruby's net/http rejects it
    # because OpenSSL can't link the leaf to a trusted root. We bundle the
    # intermediate at config/rbv_ca_bundle.pem and load it into the cert store
    # at request time rather than disabling verification (same approach as BoA).
    class RBV < Adapter
      URL = "https://www.rbv.gov.vu/index.php/en/exchange-rates"
      PAGE_SIZE = 100_000
      CA_BUNDLE = File.expand_path("../../../config/rbv_ca_bundle.pem", __dir__)

      QUOTE_COLUMNS = ["usd", "jpy", "nzd", "GBP", "aud", "eur"].freeze

      def fetch(after: nil, upto: nil)
        records = parse(http_get)
        records.select! { |r| r[:date] > after } if after
        records.select! { |r| r[:date] <= upto } if upto
        records
      end

      def parse(html)
        doc = Nokogiri::HTML.parse(html)

        doc.css("tr.fabrik_row").flat_map do |row|
          date_text = row.at_css("td.exchange_rates___date")&.text&.strip
          next [] unless date_text && !date_text.empty?

          date = parse_date(date_text)
          next [] unless date

          QUOTE_COLUMNS.filter_map do |code|
            cell = row.at_css("td.exchange_rates___#{code}")
            next unless cell

            rate = Float(cell.text.strip, exception: false)
            next unless rate&.positive?

            { date:, base: code.upcase, quote: "VUV", rate: }
          end
        end
      end

      private

      def parse_date(text)
        Date.parse(text)
      rescue Date::Error
        nil
      end

      # www.rbv.gov.vu serves a malformed chain that omits its issuer (the Trustico
      # RSA DV SSL CA 2 intermediate), so the default trust store can't build a
      # chain to a root. We augment it with the bundled intermediate instead of
      # disabling verification.
      def http_get
        http.get(URL, params: { limit1: PAGE_SIZE }, ssl_context: ssl_context).to_s
      end

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
