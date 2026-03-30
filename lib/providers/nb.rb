# frozen_string_literal: true

require "csv"
require "net/http"

require "providers/base"

module Providers
  class NB < Base
    SDMX_URL = "https://data.norges-bank.no/api/data/EXR/B..NOK.SP"
    EARLIEST_DATE = Date.new(1999, 1, 4)

    class << self
      def key = "NB"
      def name = "Norges Bank"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 365)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      url = URI(SDMX_URL)
      params = { format: "csvdata" }
      params[:startPeriod] = since.to_s if since
      params[:endPeriod] = upto.to_s if upto
      url.query = URI.encode_www_form(params)

      @dataset = []
      stream_csv(url) do |row|
        record = parse_row(row)
        @dataset << record if record
      end

      self
    rescue Net::OpenTimeout, Net::ReadTimeout
      self
    end

    def parse(csv)
      CSV.parse(csv, headers: true, liberal_parsing: true).filter_map do |row|
        parse_row(row)
      end
    end

    private

    def parse_row(row)
      return unless row["FREQ"] == "B"

      base = row["BASE_CUR"]
      return unless base

      rate = Float(row["OBS_VALUE"])
      unit_mult = Integer(row["UNIT_MULT"] || "0")
      rate /= 10**unit_mult if unit_mult > 0
      date = Date.parse(row["TIME_PERIOD"])

      { provider: key, date:, base:, quote: "NOK", rate: }
    rescue ArgumentError, TypeError
      nil
    end

    def stream_csv(url)
      uri = URI(url)
      Net::HTTP.start(uri.host, uri.port, use_ssl: true) do |http|
        request = Net::HTTP::Get.new(uri)
        http.request(request) do |response|
          headers = nil
          buffer = +""

          response.read_body do |chunk|
            buffer << chunk
            while (line_end = buffer.index("\n"))
              line = buffer.slice!(0..line_end)
              if headers.nil?
                headers = CSV.parse_line(line, liberal_parsing: true)
              else
                values = CSV.parse_line(line, liberal_parsing: true)
                next unless values

                row = CSV::Row.new(headers, values)
                yield row
              end
            end
          end

          # Process remaining buffer
          if headers && !buffer.empty?
            values = CSV.parse_line(buffer, liberal_parsing: true)
            yield CSV::Row.new(headers, values) if values
          end
        end
      end
    end
  end
end
