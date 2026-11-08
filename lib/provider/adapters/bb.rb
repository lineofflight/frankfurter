# frozen_string_literal: true

require "date"
require "nokogiri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bangladesh Bank. Publishes the exchange rate of the taka each business morning, Sunday to Thursday: the USD/BDT
    # bid and ask (the highest and lowest interbank rates) and, since May 2024, the weighted average rate (WAR), plus
    # bid and ask cross rates for ten more currencies derived from interbank deals reported to BAFEDA. Rows are dated by
    # the day the sheet takes effect and carry the previous business day's Dhaka close. A currency with no interbank
    # deal that day is left blank. A few days in 2009 and 2011 carry a cross rate mistyped or in the wrong column; those
    # are relayed as published for the blend's spike screen to catch.
    #
    # The page's search form posts a month and returns that month's daily table, so a fetch is one POST per month. The
    # archive starts with two stray rows in March and May 2006 and runs daily from June 2006.
    #
    # Rates are BDT per unit of foreign currency, so foreign goes in base and BDT in quote, except LKR, which the sheet
    # quotes as LKR per taka. USD takes the WAR as its mid where published; otherwise bid and ask resolve to a mid.
    #
    # Terms: none published. The site carries only a disclaimer of liability.
    class BB < Adapter
      URL = "https://www.bb.org.bd/en/index.php/econdata/exchangerate"
      COVERAGE_START = Date.new(2006, 3, 21)
      # Quoted as foreign currency per taka.
      INVERSE = ["LKR"].freeze
      DATE = %r{\A\d{2}/\d{2}/\d{2}\z}

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        start_date = [after ? after + 1 : COVERAGE_START, COVERAGE_START].max
        end_date = upto || Date.today
        return [] if start_date > end_date

        records = months(start_date, end_date).each_with_index.flat_map do |month, index|
          sleep(1) if index.positive?
          parse(http.post(URL, form: { currencies: "all", date_picker: month.strftime("%B, %Y") }).to_s)
        end

        records.select { |r| r[:date].between?(start_date, end_date) }
      end

      def parse(html)
        doc = Nokogiri::HTML.parse(html)
        header = doc.at_css("tr.header-top")
        raise "BB: no rates table on #{URL}" unless header

        columns = header.css("th").drop(1).flat_map { |th| [th.text.strip] * (th["colspan"] || 1).to_i }
        labels = header.next_element.css("td").drop(1).map { |td| td.text.strip }

        doc.css("tbody tr").flat_map do |tr|
          cells = tr.css("td").map { |td| td.text.strip }
          next [] unless cells.first&.match?(DATE)

          date = Date.strptime(cells.first, "%d/%m/%y")
          quotes = Hash.new { |hash, code| hash[code] = {} }
          columns.zip(labels, cells.drop(1)).each do |code, label, value|
            number = Float(value.to_s, exception: false)
            quotes[code][label] = number if number&.positive?
          end

          quotes.filter_map do |code, values|
            bid, ask, war = values.values_at("Bid Rate", "Ask Rate", "WAR")
            rate = war || midpoint(bid, ask)
            next unless rate

            base, quote = INVERSE.include?(code) ? ["BDT", code] : [code, "BDT"]
            { date:, base:, quote:, rate:, **prices(bid:, ask:, mid: war) }
          end
        end
      end

      private

      def months(start_date, end_date)
        (start_date..end_date).map { |date| Date.new(date.year, date.month) }.uniq
      end
    end
  end
end
