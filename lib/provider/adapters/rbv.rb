# frozen_string_literal: true

require "date"
require "net/http"
require "nokogiri"

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
    class RBV < Adapter
      URL = "https://www.rbv.gov.vu/index.php/en/exchange-rates"
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"
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
        uri = URI(URL)
        uri.query = URI.encode_www_form(limit1: PAGE_SIZE)

        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = uri.scheme == "https"
        http.open_timeout = 30
        http.read_timeout = 60

        req = Net::HTTP::Get.new(uri)
        req["User-Agent"] = USER_AGENT
        req["Accept"] = "text/html"

        response = http.request(req)
        response.value
        response.body
      end
    end
  end
end
