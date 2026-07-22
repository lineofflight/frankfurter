# frozen_string_literal: true

require "nokogiri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Reserve Bank of Malawi. Publishes daily buy/middle/sell rates against
    # MWK for ~38 currencies through an ASP.NET MVC site backed by IIS.
    #
    # The historical endpoint is a POST form that accepts US-formatted
    # StartDate / EndDate (MM/DD/YYYY) and an optional RateTypes filter
    # (omit to return all currencies). The response is an HTML page; each
    # row carries the currency code in <strong>, then three numeric cells
    # (Buying, Middle, Selling) and a date cell ("Jan 02 2024" with a pair
    # of non-breaking spaces between the day and the year).
    #
    # We POST 30-day windows and parse the result table. RBM publishes
    # "1 foreign = X MWK", so foreign is the base and MWK the quote.
    # The source already publishes a middle column, so we use that
    # directly instead of recomputing from buy/sell (issue #314).
    #
    # IEP (defunct Irish punt) and CMD (not a real ISO 4217 code) appear
    # in the response. CMD is unknown to Money::Currency and is dropped by
    # Provider#backfill's default filter. IEP is registered via
    # db/seeds/currency_patches.json (to support pre-euro Bundesbank data),
    # so Money::Currency.find recognises it and Provider#backfill passes it
    # through.
    class RBM < Adapter
      URL = "https://www.rbm.mw/Statistics/ExchangeRatesFilter/"

      MONTHS = {
        "Jan" => 1,
        "Feb" => 2,
        "Mar" => 3,
        "Apr" => 4,
        "May" => 5,
        "Jun" => 6,
        "Jul" => 7,
        "Aug" => 8,
        "Sep" => 9,
        "Oct" => 10,
        "Nov" => 11,
        "Dec" => 12,
      }.freeze

      CODE_RE = /\A[A-Z]{3}\z/
      DATE_RE = /\A([A-Z][a-z]{2})\s+(\d{1,2})\s+(\d{4})\z/

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2011, 6, 20)
        end_date = upto || Date.today
        return [] if start_date > end_date

        html = post_range(start_date, end_date)
        parse(html)
      end

      def parse(html)
        doc = Nokogiri::HTML.parse(html)
        table = doc.at_css("table#exchange-rates") || doc.at_css("table")
        return [] unless table

        table.css("tr").filter_map do |row|
          cells = row.css("td")
          next if cells.length < 5

          code = cells[0].text.strip
          next unless CODE_RE.match?(code)

          middle = parse_number(cells[2].text)
          next if middle.nil? || middle.zero?

          date = parse_date(cells[4].text)
          next unless date

          { date:, base: code, quote: "MWK", rate: middle }
        end
      end

      private

      def post_range(start_date, end_date)
        form = {
          RateTypes: "",
          StartDate: start_date.strftime("%m/%d/%Y"),
          EndDate: end_date.strftime("%m/%d/%Y"),
        }

        http.post(URL, form:).to_s
      end

      def parse_number(str)
        cleaned = str.to_s.gsub(/[[:space:]]/, "").tr(",", "")
        return if cleaned.empty?

        Float(cleaned)
      rescue ArgumentError
        nil
      end

      def parse_date(str)
        cleaned = str.to_s.gsub(/[[:space:]]+/, " ").strip
        match = cleaned.match(DATE_RE)
        return unless match

        month = MONTHS[match[1]]
        return unless month

        Date.new(match[3].to_i, month, match[2].to_i)
      rescue Date::Error
        nil
      end
    end
  end
end
