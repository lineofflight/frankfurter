# frozen_string_literal: true

require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank Al-Maghrib. Publishes daily mid-market rates for ~30 currencies against MAD.
    class BAM < Adapter
      URL = "https://api.centralbankofmorocco.ma/cours/Version1/api/CoursVirement"
      class << self
        def api_key = ENV["BAM_API_KEY"] || raise(ApiKeyMissing)

        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        (after..end_date).each do |date|
          next if date.saturday? || date.sunday?

          sleep(15) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        return [] unless data.is_a?(Array)

        data.filter_map do |record|
          code = record["libDevise"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          date = Date.parse(record["date"])
          moyen = record["moyen"].to_f
          unite = record["uniteDevise"].to_f
          next if moyen.zero? || unite.zero?

          { date:, base: code, quote: "MAD", rate: moyen / unite }
        end
      end

      private

      def fetch_date(date)
        uri = URI(URL)
        uri.query = URI.encode_www_form(date: "#{date.strftime("%Y-%m-%d")}T12:30:00")
        request = Net::HTTP::Get.new(uri)
        request["Ocp-Apim-Subscription-Key"] = self.class.api_key
        response = Net::HTTP.start(uri.hostname, uri.port, use_ssl: true) { |http| http.request(request) }
        parse(response.body)
      end
    end
  end
end
