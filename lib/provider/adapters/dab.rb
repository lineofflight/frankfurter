# frozen_string_literal: true

require "nokogiri"

require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    # Da Afghanistan Bank (DAB). Publishes daily reference exchange rates for the Afghan afghani (AFN) against ~10
    # currencies via the public exchange rates page on dab.gov.af. Coverage starts 2019-03-31 and the page accepts a
    # `field_date_value=YYYY-MM-DD` query parameter to fetch historical days.
    #
    # The page contains two tables: a daily snapshot (which we consume) and a monthly average (which we ignore). Each
    # table has four columns: Cash Sell/Buy and Transfer Sell/Buy. The transfer mid (the mean of transfer buy and
    # transfer sell) is DAB.TRANSFER and the cash mid DAB.CASH, following the mid-rate convention used by other buy/sell
    # providers.
    #
    # Row labels are descriptive strings (e.g. "USD$", "EURO€", "INDIAN Rs.", "IRAN Toman", "UAE DIRHAM"); we map them
    # to ISO 4217 codes via LABEL_MAP. "IRAN Toman" is a non-ISO unit equal to 10 IRR, so we divide the rate by 10 when
    # emitting the IRR record.
    #
    # Rates are returned in DAB's native direction: foreign currency as base, AFN as quote (e.g. 1 USD ~ 63.7 AFN),
    # matching the convention used by other pivot-in-quote adapters (NBG, BBK).
    class DAB < Adapter
      BASE_URL = "https://www.dab.gov.af/exchange-rates"

      # Maps DAB row labels (after stripping currency symbols and whitespace) to ISO 4217 codes. "IRAN TOMAN" is the
      # "IRAN Toman" row; values get divided by 10 since 1 toman = 10 IRR.
      LABEL_MAP = {
        "USD" => "USD",
        "EURO" => "EUR",
        "POUND" => "GBP",
        "SWISS" => "CHF",
        "INDIAN RS." => "INR",
        "PAKISTAN RS." => "PKR",
        "IRAN TOMAN" => "IRR",
        "CNY" => "CNY",
        "UAE DIRHAM" => "AED",
        "SAUDI RIYAL" => "SAR",
      }.freeze

      IRR_PER_TOMAN = 10.0

      # Sell and buy columns of each dataset.
      COLUMNS = { "DAB.TRANSFER" => [3, 4], "DAB.CASH" => [1, 2] }.freeze

      class << self
        # Per-day endpoint — chunk one day at a time.
        def backfill_range = 1
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        after.upto(end_date) do |date|
          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(html, date:)
        doc = Nokogiri::HTML.parse(html)
        table = doc.at_css("div.table-responsive table.table-striped")

        # Holidays render "There were no results." instead of the rates table.
        return [] if html.include?("There were no results")
        raise "DAB: no rates table for #{date} at #{BASE_URL}" unless table

        table.css("tbody tr").flat_map do |row|
          cells = row.css("td")
          next [] if cells.length < 5

          label = normalize_label(cells[0].text)
          code = LABEL_MAP[label]
          next [] unless code

          unit = code == "IRR" ? IRR_PER_TOMAN : 1
          COLUMNS.filter_map do |dataset, (sell_column, buy_column)|
            sell = parse_decimal(cells[sell_column].text)
            buy = parse_decimal(cells[buy_column].text)
            next unless sell && buy

            { dataset:, date:, base: code, quote: "AFN", rate: midpoint(sell, buy) / unit,
              **prices(bid: buy, ask: sell, unit:), }
          end
        end
      end

      private

      def normalize_label(text)
        text.strip
          .gsub(/[$€£₣¥]/, "")
          .gsub(/\s+/, " ")
          .strip
          .upcase
      end

      def parse_decimal(text)
        stripped = text.strip.delete(",")
        return if stripped.empty?

        value = Float(stripped, exception: false)
        return unless value&.positive?

        value
      end

      def fetch_date(date)
        sleep(0.5)
        parse(http.get(BASE_URL, params: { field_date_value: date.strftime("%Y-%m-%d") }).to_s, date:)
      end
    end
  end
end
