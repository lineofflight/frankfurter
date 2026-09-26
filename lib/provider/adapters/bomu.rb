# frozen_string_literal: true

require "nokogiri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Mauritius. Daily averages of banks' indicative retail transfer buy/sell rates, in MUR per foreign unit.
    # Both date filters are required: omitting the end date asks Drupal for the entire remaining archive.
    class BOMU < Adapter
      URL = "https://www.bom.mu/markets/foreign-exchange/consolidated-indicative-exchange-rates"
      COVERAGE_START = Date.new(2001, 7, 3)

      class << self
        def backfill_range = 31
      end

      def fetch(after: nil, upto: nil)
        start_date = after || COVERAGE_START
        end_date = upto || Date.today
        records = []
        cursor = start_date
        while cursor <= end_date
          last = [cursor + self.class.backfill_range - 1, end_date].min
          params = {
            "field_transaction_date_value[value][date]" => cursor.strftime("%d-%m-%Y"),
            "field_transaction_date_value_1[value][date]" => last.strftime("%d-%m-%Y"),
          }
          sleep(0.2) unless cursor == start_date
          records.concat(parse(http.get(URL, params:).to_s))
          cursor = last + 1
        end
        records.select { |r| r[:date].between?(start_date, end_date) }
      end

      def parse(html)
        # The live page contains a NUL in its navigation, which otherwise truncates Nokogiri's HTML parser.
        doc = Nokogiri::HTML(html.delete("\0"))
        view = doc.at_css(".view-display-id-page")
        raise "BOMU: missing exchange-rate view" unless view

        # An attachment repeats the filtered rates and a sidebar shows today's quotes. Use only the primary table.
        view.css("> .view-content > .table-responsive > table > tbody > tr.tblConso").filter_map do |row|
          parse_row(row)
        end
      end

      private

      def parse_row(row)
        label = row.at_css(".views-field-field-currency")&.text.to_s.strip
        match = label.match(/\A([A-Z]{3})\s+(\d+)\z/)
        return unless match

        code, unit = match.captures
        unit = unit.to_i
        return unless unit.positive?

        buy = Float(row.at_css(".views-field-php")&.text.to_s.strip, exception: false)
        sell = Float(row.at_css(".views-field-php-3")&.text.to_s.strip, exception: false)
        return unless buy&.positive? && sell&.positive?

        date = Date.strptime(row.at_css(".views-field-field-transaction-date").text.strip, "%d-%m-%Y")
        components = prices(bid: buy, ask: sell, unit:)
        { date:, base: code, quote: "MUR", rate: midpoint(components[:bid], components[:ask]), **components }
      end
    end
  end
end
