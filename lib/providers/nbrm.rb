# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # National Bank of the Republic of North Macedonia. Publishes daily mid-market rates for ~31 currencies against MKD.
  class NBRM < Base
    BASE_URL = "https://www.nbrm.mk/KLServiceNOV/GetExchangeRate"
    EARLIEST_DATE = Date.new(2005, 1, 3)
    CHUNK_DAYS = 90

    class << self
      def key = "NBRM"
      def name = "National Bank of North Macedonia"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      end_date = upto || Date.today
      end_date = Date.parse(end_date.to_s)
      @dataset = []

      each_chunk(start_date, end_date) do |chunk_start, chunk_end|
        @dataset.concat(fetch_rates(chunk_start, chunk_end))
      end

      self
    end

    def parse(json)
      data = json.is_a?(String) ? JSON.parse(json) : json
      return [] unless data.is_a?(Array)

      data.filter_map do |row|
        iso = row["oznaka"]&.strip
        next unless iso&.match?(/\A[A-Z]{3}\z/)
        next if iso == "MKD"

        rate = Float(row["sreden"]) / Integer(row["nomin"])
        next if rate.zero?

        date = Date.strptime(row["datum"].split("T").first, "%Y-%m-%d")

        { provider: key, date:, base: iso, quote: "MKD", rate: }
      end
    end

    private

    def fetch_rates(start_date, end_date)
      url = URI(BASE_URL)
      url.query = URI.encode_www_form(
        StartDate: start_date.strftime("%d.%m.%Y"),
        EndDate: end_date.strftime("%d.%m.%Y"),
        format: "json",
      )

      response = Net::HTTP.get(url)
      parse(response)
    rescue JSON::ParserError, Net::OpenTimeout, Net::ReadTimeout, Errno::ECONNRESET
      []
    end

    def each_chunk(start_date, end_date)
      current = start_date
      first = true
      while current <= end_date
        sleep(0.5) unless first
        first = false
        chunk_end = [current + CHUNK_DAYS - 1, end_date].min
        yield current, chunk_end
        current = chunk_end + 1
      end
    end
  end
end
