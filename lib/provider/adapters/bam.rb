# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank Al-Maghrib. Publishes daily mid-market rates for ~30 currencies against MAD.
    # Older data (pre-2016) lacks a mid-rate field; the adapter averages buy/sell.
    class BAM < Adapter
      URL = "https://api.centralbankofmorocco.ma/cours/Version1/api/CoursVirement"
      class << self
        def api_key = ENV["BAM_API_KEY"] || raise(Adapter::Unavailable, "no API key")

        def backfill_range = 7
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today

        (after..end_date).each_with_object([]) do |date, dataset|
          next if date.saturday? || date.sunday?

          dataset.concat(fetch_date(date))
        end
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        return [] unless data.is_a?(Array)

        data.filter_map do |record|
          code = record["libDevise"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          date = Date.parse(record["date"])
          mid = record["moyen"]&.to_f || ((record["achat"].to_f + record["vente"].to_f) / 2).round(4)
          unite = record["uniteDevise"].to_f
          next if mid.zero? || unite.zero?

          { date:, base: code, quote: "MAD", rate: mid / unite }
        end
      end

      private

      def fetch_date(date)
        uri = URI(URL)
        uri.query = URI.encode_www_form(date: "#{date.strftime("%Y-%m-%d")}T12:30:00")
        request = Net::HTTP::Get.new(uri)
        request["Ocp-Apim-Subscription-Key"] = self.class.api_key

        loop do
          response = Net::HTTP.start(uri.hostname, uri.port, use_ssl: true) { |http| http.request(request) }

          if response.is_a?(Net::HTTPTooManyRequests)
            sleep(response["retry-after"].to_i)
          else
            sleep(1)
            response.value
            return parse(response.body)
          end
        end
      end
    end
  end
end
