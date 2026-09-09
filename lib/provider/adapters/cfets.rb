# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # China Foreign Exchange Trade System. The PBOC's interbank platform publishes the daily RMB central parity rate
    # (the official fixing, 09:15 Beijing) for 25 pairs. The history endpoint returns every pair when `currency` is
    # blank, names them in `data.head` (e.g. "USD/CNY", "100JPY/CNY", "CNY/THB") and aligns each record's `values` to
    # it. Direction is per label: the first ten pairs are foreign-per-CNY, the rest CNY-per-foreign. Missing values are
    # "---". The endpoint refuses spans of a year or more and any pageSize above 50.
    class CFETS < Adapter
      URL = "https://www.chinamoney.com.cn/ags/ms/cm-u-bk-ccpr/CcprHisNew"

      CHUNK_DAYS = 60
      PAGE_SIZE = 50

      class << self
        def backfill_range = CHUNK_DAYS
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        start_date = after || (end_date - CHUNK_DAYS + 1)
        dataset = []
        page = 1

        loop do
          sleep(0.5) if page > 1
          data = Oj.load(fetch_page(start_date, end_date, page), mode: :strict)
          dataset.concat(parse(data))
          break if page >= data.dig("data", "pageTotal").to_i

          page += 1
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        raise "CFETS: #{data.dig("data", "flagMessage")}" if data["records"].nil?

        pairs = Array(data.dig("data", "head")).map { |label| parse_label(label) }

        Array(data["records"]).flat_map do |record|
          date = Date.parse(record["date"])

          pairs.zip(Array(record["values"])).filter_map do |pair, value|
            next unless pair
            next unless value&.match?(/\A\d+(\.\d+)?\z/)

            rate = Float(value) / pair[:unit]
            next if rate.zero?

            { date:, base: pair[:base], quote: pair[:quote], rate: }
          end
        end
      end

      private

      def fetch_page(start_date, end_date, page)
        params = {
          startDate: start_date.iso8601,
          endDate: end_date.iso8601,
          currency: "",
          pageNum: page,
          pageSize: PAGE_SIZE,
        }
        http.post(URL, params:).to_s
      end

      # "100JPY/CNY" => { base: "JPY", quote: "CNY", unit: 100 }
      def parse_label(label)
        match = label.to_s.match(%r{\A(\d*)([A-Z]{3})/(\d*)([A-Z]{3})\z})
        return unless match

        base_unit, base, quote_unit, quote = match.captures
        return unless quote_unit.empty?

        unit = base_unit.empty? ? 1 : base_unit.to_i
        return if unit.zero?

        { base:, quote:, unit: }
      end
    end
  end
end
