# frozen_string_literal: true

require "net/http"
require "nokogiri"

require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    # National Bank of Cambodia. Publishes daily reference rates for ~29 currencies against
    # the Cambodian riel (KHR), Mon-Fri ~16:30 Asia/Phnom Penh.
    #
    # The page is form-based: a GET returns a hidden CSRF token (tk) and sets a session
    # cookie; a POST with `exdate`, `tk`, `view=View` returns that date's HTML table. The
    # token rotates per request, so every historical fetch is a GET-then-POST round trip.
    #
    # Rates are quoted as `<CCY>/KHR` with a unit multiplier (1, 100, 1000) and bid/ask/average
    # columns. We use the published `average` column as the mid and divide by the unit to
    # normalize to per-1 rates. The cross-rate table omits USD; we read the headline
    # "KHR / USD" official exchange rate separately.
    #
    # Records are returned in NBC's native direction — foreign currency as base, KHR as
    # quote — matching the convention used by other pivot-in-quote adapters (e.g. NBG, BBK).
    #
    # SDR is excluded (composite unit, not an ISO currency).
    class NBC < Adapter
      BASE_URL = "https://www.nbc.gov.kh/english/economic_research/exchange_rate.php"
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"
      SYMBOL_PATTERN = %r{\A([A-Z]{3})/KHR\z}
      OER_PATTERN = /\A(\d+)\z/
      EXCLUDED_QUOTES = ["SDR"].freeze

      class << self
        # Per-day endpoint with CSRF round-trip — keep chunks tiny.
        def backfill_range = 1
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        after.upto(end_date) do |date|
          next if date.sunday?

          sleep(0.5) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(html, date:)
        return [] unless html.include?("<table") && !empty_response?(html)

        doc = Nokogiri::HTML.parse(html)

        records = doc.css("tr").filter_map do |row|
          cells = row.css("td")
          next if cells.length < 6

          code = cells[1].text.strip[SYMBOL_PATTERN, 1]
          next unless code
          next if EXCLUDED_QUOTES.include?(code)

          unit = Integer(cells[2].text.strip, exception: false)
          next unless unit&.nonzero?

          average = Float(cells[5].text.strip.delete(","), exception: false)
          next unless average&.nonzero?

          { date:, base: code, quote: "KHR", rate: average / unit }
        end

        oer_rate = extract_oer(doc)
        records << { date:, base: "USD", quote: "KHR", rate: oer_rate } if oer_rate

        records
      end

      private

      def extract_oer(doc)
        doc.css("font").each do |font|
          parent_text = font.parent&.text.to_s
          next unless parent_text.include?("KHR") && parent_text.include?("USD")

          digits = font.text.strip[OER_PATTERN, 1]
          next unless digits

          rate = Float(digits)
          return rate if rate.nonzero?
        end
        nil
      end

      def empty_response?(html)
        html.include?("There is no data available")
      end

      def fetch_date(date)
        page, cookies = load_page
        token = extract_token(page)
        raise "NBC: CSRF token not found on landing page" unless token

        body = post_date(date:, token:, cookies:)
        parse(body, date:)
      end

      def extract_token(html)
        doc = Nokogiri::HTML.parse(html)
        input = doc.at_css("input[name='tk']")
        input&.[]("value")
      end

      def load_page
        uri = URI(BASE_URL)
        http = build_http(uri)
        req = Net::HTTP::Get.new(uri)
        req["User-Agent"] = USER_AGENT
        resp = http.request(req)
        cookies = resp.get_fields("set-cookie")&.map { |c| c.split(";").first }&.join("; ") || ""
        [resp.body, cookies]
      end

      def post_date(date:, token:, cookies:)
        uri = URI(BASE_URL)
        http = build_http(uri)
        req = Net::HTTP::Post.new(uri)
        req["User-Agent"] = USER_AGENT
        req["Cookie"] = cookies unless cookies.empty?
        req["Referer"] = BASE_URL
        req["Content-Type"] = "application/x-www-form-urlencoded"
        req.body = URI.encode_www_form(
          "exdate" => date.strftime("%Y-%m-%d"),
          "tk" => token,
          "view" => "View",
        )
        http.request(req).body
      end

      def build_http(uri)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = uri.scheme == "https"
        http.open_timeout = 30
        http.read_timeout = 60
        http
      end
    end
  end
end
