# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Tanzania. Publishes daily exchange rates for 35+ currencies
    # against the Tanzanian shilling (TZS). Uses the Mean column (midpoint
    # of buy/sell). Publishes 7 days a week.
    class BOTA < Adapter
      BASE_URL = "https://www.bot.go.tz"
      EXCLUDED_CURRENCIES = ["GOLD", "ATS", "NLG", "MZM", "ZWD", "CUC"].freeze

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        uri = URI("#{BASE_URL}/ExchangeRate/previous_rates")
        response = Net::HTTP.post_form(uri, {
          "dateFrom" => after.strftime("%m/%d/%Y"),
          "dateTo" => (upto || Date.today).strftime("%m/%d/%Y"),
        })

        sleep(2)
        parse(response.body)
      end

      def parse(html)
        doc = Ox.load(html, mode: :generic, effort: :tolerant, smart: true)
        rows = find_table_rows(doc)
        return [] unless rows

        rows.filter_map { |row| parse_row(row) }
      end

      private

      def find_table_rows(node)
        return unless node.respond_to?(:nodes)

        if node.respond_to?(:value) && node.value == "tbody"
          return node.nodes&.select { |n| n.respond_to?(:value) && n.value == "tr" }
        end

        node.nodes&.each do |child|
          result = find_table_rows(child)
          return result if result
        end

        nil
      end

      def parse_row(row)
        cells = row.nodes&.select { |n| n.respond_to?(:value) && n.value == "td" }
        return unless cells && cells.size >= 6

        currency = cell_text(cells[1])&.strip&.upcase
        return unless currency
        return if EXCLUDED_CURRENCIES.include?(currency)

        mean_str = cell_text(cells[4])
        date_str = cell_text(cells[5])
        return unless mean_str && date_str

        rate = Float(mean_str.tr(",", ""))
        return if rate.zero?

        date = Date.parse(date_str.strip)

        { date:, base: currency, quote: "TZS", rate: }
      end

      def cell_text(node)
        return unless node

        texts = []
        collect_text(node, texts)
        text = texts.join
        text.empty? ? nil : text
      end

      def collect_text(node, texts)
        if node.is_a?(String)
          texts << node
        elsif node.respond_to?(:nodes) && node.nodes
          node.nodes.each { |child| collect_text(child, texts) }
        end
      end
    end
  end
end
