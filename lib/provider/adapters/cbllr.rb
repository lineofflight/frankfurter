# frozen_string_literal: true

require "date"
require "net/http"
require "nokogiri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Liberia (CBL). Publishes a daily indicative exchange rate
    # for the US dollar against the Liberian dollar (LRD) on a Drupal-rendered
    # HTML page. CBL is the only national authority quoting LRD; Liberia
    # operates a dollarized economy alongside the local currency.
    #
    # The bare acronym "CBL" already collides with the Central Bank of Libya
    # (#394, declined), so the adapter key smushes in the country code: CBLLR.
    #
    # The page lists ~14 entries per page sorted newest-first, with a ?page=N
    # 0-indexed pager reaching back to 2012-07-05. The full archive is ~105
    # pages. The adapter walks pages newest-first and stops once it sees a row
    # older than the requested `after`, so incremental fetches usually touch
    # just one page.
    #
    # CBL publishes buy and sell prices ("L$X/US$1.00"); we coerce to the mid
    # (issue #314). Rates are returned in CBL's native direction — USD as base,
    # LRD as quote — matching the convention used by other USD-pivoted
    # single-pair adapters (e.g. BANREP, BCCR, MMA).
    class CBLLR < Adapter
      BASE_URL = "https://www.cbl.org.lr/research/buying-selling-rates"
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"
      RATE_PATTERN = %r{L\$\s*([\d.]+)\s*/\s*US\$}

      # Hard ceiling to avoid runaway pagination if the markup ever changes.
      # The full archive is ~105 pages today; 500 gives years of headroom.
      MAX_PAGES = 500

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        records = []

        MAX_PAGES.times do |page|
          html = fetch_page(page)
          page_records = parse(html)
          break if page_records.empty?

          records.concat(page_records)

          oldest = page_records.map { |r| r[:date] }.min
          break if after && oldest <= after

          sleep(0.5)
        end

        records.select! { |r| r[:date] > after } if after
        records.select! { |r| r[:date] <= end_date }
        records.uniq! { |r| [r[:date], r[:base], r[:quote]] }
        records.sort_by! { |r| r[:date] }
        records
      end

      def parse(html)
        doc = Nokogiri::HTML.parse(html)

        doc.css(".view-content table tr").filter_map do |row|
          time = row.at_css("td.views-field-field-content-post-date time")
          buy_cell = row.at_css("td.views-field-field-buying-us")
          sell_cell = row.at_css("td.views-field-field-selling-us")
          next unless time && buy_cell && sell_cell

          datetime = time["datetime"]
          next unless datetime

          date = Date.parse(datetime)

          buy = extract_rate(buy_cell.text)
          sell = extract_rate(sell_cell.text)
          next unless buy && sell

          mid = (buy + sell) / 2.0
          next unless mid.positive?

          { date:, base: "USD", quote: "LRD", rate: mid }
        end
      end

      private

      def extract_rate(text)
        match = text.match(RATE_PATTERN)
        return unless match

        Float(match[1], exception: false)
      end

      def fetch_page(page)
        uri = URI(BASE_URL)
        uri.query = URI.encode_www_form(page: page) if page > 0

        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = true
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
