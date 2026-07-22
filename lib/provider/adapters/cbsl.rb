# frozen_string_literal: true

require "nokogiri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Sri Lanka. Publishes daily indicative exchange rates for 55 currencies
    # (including XAU per troy ounce) against the Sri Lankan rupee (LKR). Indicative rates are
    # derived at the start of business (09:30 Colombo time, UTC+5:30) based on world currency
    # rates against the US dollar and the USD/LKR spot rate.
    #
    # The endpoint is a PHP form handler that returns an HTML page with one table per
    # selected currency. We POST all 55 currencies in one request and parse each table
    # by associating the header (e.g. "1 USD -> LKR") with the data rows.
    #
    # Records are returned in CBSL's native direction — foreign currency as base, LKR as
    # quote — matching the convention used by other pivot-in-quote adapters (e.g. NBG, BBK).
    # XAU is published per troy ounce, matching Frankfurter's convention.
    class CBSL < Adapter
      ENDPOINT = URI("https://www.cbsl.gov.lk/cbsl_custom/exrates/exrates_results.php")
      FORM_PAGE = URI("https://www.cbsl.gov.lk/cbsl_custom/exrates/exrates.php")

      class << self
        # The endpoint comfortably returns a year of all 55 currencies (~1 MB) in under
        # two seconds, so chunk the backfill yearly.
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        currencies = fetch_currencies
        html = post_request(after, end_date, currencies)

        parse(html)
      end

      def parse(html)
        doc = Nokogiri::HTML.parse(html)
        records = []

        doc.css("table").each do |table|
          code = currency_code(table)
          next unless code

          table.css("tbody tr").each do |row|
            cells = row.css("td")
            next if cells.length < 2

            date_str = cells[0].text.strip
            rate_str = cells[1].text.strip
            next unless date_str.match?(/\A\d{4}-\d{2}-\d{2}\z/)

            rate = Float(rate_str, exception: false)
            next unless rate&.positive?

            records << { date: Date.parse(date_str), base: code, quote: "LKR", rate: rate }
          end
        end

        records
      end

      private

      def currency_code(table)
        table.css("thead th").each do |th|
          match = th.text.match(/\A\s*1\s+([A-Z]{3})\s+->\s+LKR\s*\z/)
          return match[1] if match
        end
        nil
      end

      def fetch_currencies
        doc = Nokogiri::HTML.parse(http.get(FORM_PAGE).to_s)
        doc.css('input[name="chk_cur[]"]').filter_map { |input| input["value"] }
      end

      def post_request(after, upto, currencies)
        form = [
          ["lookupPage", "lookup_daily_exchange_rates.php"],
          ["rangeType", "dates"],
          ["txtStart", after.to_s],
          ["txtEnd", upto.to_s],
        ]
        currencies.each { |value| form << ["chk_cur[]", value] }
        form << ["submit_button", "Submit"]

        http.post(ENDPOINT, form:).to_s
      end

    end
  end
end
