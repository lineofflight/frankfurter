# frozen_string_literal: true

require "net/http"

require "providers/base"

module Providers
  # Bank of Botswana. Publishes daily rates for ~7 currencies against BWP.
  class BOB < Base
    CSV_URL = URI("https://www.bankofbotswana.bw/export/exchange-rates.csv?page&_format=csv")

    # Columns in the CSV mapped to ISO currency codes
    COLUMNS = {
      "CHN" => "CNY",
      "EUR" => "EUR",
      "GBP" => "GBP",
      "USD" => "USD",
      "YEN" => "JPY",
      "ZAR" => "ZAR",
    }.freeze

    def key = "BOB"
    def name = "Bank of Botswana"
    def base = "BWP"

    def current
      @dataset = parse(Net::HTTP.get(CSV_URL))
      last_date = @dataset.last&.dig(:date)
      @dataset = @dataset.select { |r| r[:date] == last_date }
      self
    end

    def historical
      @dataset = parse(Net::HTTP.get(CSV_URL))
      self
    end

    def parse(csv)
      csv = csv.dup.force_encoding(Encoding::UTF_8)
      rows = csv.delete_prefix("\uFEFF").lines(chomp: true)
      headers = rows.shift&.split(",")
      date_index = headers&.index("Date")
      return [] unless date_index

      column_indices = COLUMNS.each_with_object({}) do |(col, iso), map|
        idx = headers.index(col)
        map[iso] = idx if idx
      end

      rows.filter_map do |line|
        values = line.delete('"').split(",")
        date = Date.strptime(values[date_index].strip, "%d %b %Y")
        rates = column_indices.filter_map do |iso, idx|
          val = values[idx]
          next if val.nil? || val.empty?

          rate = Float(val)
          next if rate.zero?

          { provider: key, date:, base:, quote: iso, rate: }
        end
        rates.empty? ? nil : rates
      end.flatten.sort_by { |r| r[:date] }
    end
  end
end
