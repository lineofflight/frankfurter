# frozen_string_literal: true

require "csv"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    class ECB < Adapter
      SDMX_URL = "https://data-api.ecb.europa.eu/service/data/EXR/D..EUR.SP00.A"
      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        url = URI(SDMX_URL)
        params = { format: "csvdata" }
        params[:startPeriod] = after.to_s if after
        params[:endPeriod] = upto.to_s if upto
        url.query = URI.encode_www_form(params)

        dataset = []
        stream_csv(url) do |row|
          record = parse_row(row)
          dataset << record if record
        end

        dataset
      end

      def parse(csv)
        CSV.parse(csv, headers: true, liberal_parsing: true).filter_map do |row|
          parse_row(row)
        end
      end

      private

      def parse_row(row)
        return unless row["FREQ"] == "D"

        quote = row["CURRENCY"]
        return unless quote&.match?(/\A[A-Z]{3}\z/)

        value = row["OBS_VALUE"]
        return unless value

        rate = Float(value)
        date = Date.parse(row["TIME_PERIOD"])

        { date:, base: "EUR", quote:, rate: }
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
end
