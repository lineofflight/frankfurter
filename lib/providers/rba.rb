# frozen_string_literal: true

require "csv"
require "net/http"

require "providers/base"

module Providers
  class RBA < Base
    CSV_URL = "https://www.rba.gov.au/statistics/tables/csv/f11.1-data.csv"
    METADATA_ROWS = 11

    class << self
      def key = "RBA"
      def name = "Reserve Bank of Australia"
    end

    def fetch(since: nil, upto: nil)
      csv = Net::HTTP.get(URI(CSV_URL))
      @dataset = parse(csv)
      @dataset = dataset.select { |r| r[:date] >= since } if since
      self
    end

    def parse(csv)
      lines = csv.encode("UTF-8", invalid: :replace, undef: :replace).lines
      units_line = lines.find { |l| l.start_with?("Units,") }
      return [] unless units_line

      currencies = CSV.parse_line(units_line)
      currencies.shift # remove "Units" label

      data_lines = lines.drop(METADATA_ROWS)

      data_lines.flat_map do |line|
        row = CSV.parse_line(line)
        next unless row&.first&.match?(/\A\d{2}-[A-Za-z]{3}-\d{4}\z/)

        date = Date.parse(row.shift)

        row.zip(currencies).filter_map do |value, iso|
          next if iso.nil? || iso == "Index"
          next if value.nil? || value.strip.empty?

          rate = Float(value)
          { provider: key, date:, base: "AUD", quote: iso, rate: }
        rescue ArgumentError
          nil
        end
      end.compact
    end
  end
end
