# frozen_string_literal: true

require "date"
require "nokogiri"

require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    # Reserve Bank of Vanuatu. Publishes daily reference rates against the Vanuatu vatu (VUV) on business days,
    # 08:30-09:00 Pacific/Efate. Six quote currencies (USD, JPY, NZD, GBP, AUD, EUR), the VUV trade-weighted basket.
    #
    # The exchange-rates page is a Joomla Fabrik list. A CSV export endpoint exists but is hard-capped at 100 rows per
    # call, so we scrape the HTML list directly with a `limit1` query param large enough to return every row in one
    # response. Rows render with the date in either "DD Month YYYY" or "DD-Mon-YY" form depending on age; Date.parse
    # handles both.
    #
    # Rates are VUV per 1 unit of foreign currency. JPY is published per single unit (not per 100), so no normalization
    # is needed.
    #
    # TLS quirk: www.rbv.gov.vu omits its Trustico intermediate; see config/ca_bundles.
    class RBV < Adapter
      URL = "https://www.rbv.gov.vu/index.php/en/exchange-rates"
      PAGE_SIZE = 100_000

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

      def http_get
        http.get(URL, params: { limit1: PAGE_SIZE }).to_s
      end
    end
  end
end
