# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # National Bank of Poland Table B. Publishes weekly mid-market rates for
  # ~150 currencies against PLN every Wednesday. Rates are point-in-time
  # snapshots calculated from that Wednesday's EUR/PLN rate and market EUR
  # cross rates — not weekly averages.
  #
  # Methodology: NBP Resolution 44/2025 (Poz. 30), §2.1.3 and §3.1
  # Full text available at https://dzu.nbp.pl
  class NBPB < Base
    BASE_URL = "https://api.nbp.pl/api/exchangerates/tables/B"
    EARLIEST_DATE = Date.new(2002, 1, 2)

    class << self
      def key = "NBP.B"
      def name = "National Bank of Poland"
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      end_date = Date.today
      @dataset = []

      each_chunk(start_date, end_date) do |chunk_start, chunk_end|
        @dataset.concat(fetch_rates(chunk_start, chunk_end))
      end

      self
    end

    def parse(json)
      data = json.is_a?(String) ? JSON.parse(json) : json

      data.flat_map do |table|
        date = Date.parse(table["effectiveDate"])
        table["rates"].filter_map do |rate|
          iso = rate["code"]
          mid = rate["mid"]
          next unless iso.match?(/\A[A-Z]{3}\z/)
          next if mid.nil? || mid.zero?

          { provider: key, date:, base: iso, quote: "PLN", rate: mid }
        end
      end
    end

    private

    def fetch_rates(start_date, end_date)
      url = URI("#{BASE_URL}/#{start_date}/#{end_date}/?format=json")
      response = Net::HTTP.get(url)
      parse(response)
    rescue JSON::ParserError
      []
    end

    # NBP API limits queries to 93 days per request
    def each_chunk(start_date, end_date)
      current = start_date
      first = true
      while current <= end_date
        sleep(0.5) unless first
        first = false
        chunk_end = [current + 92, end_date].min
        yield current, chunk_end
        current = chunk_end + 1
      end
    end
  end
end
