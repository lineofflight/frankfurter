# frozen_string_literal: true

require "net/http"
require "oj"

require "providers/base"

module Providers
  # Bank Al-Maghrib. Publishes daily mid-market rates for ~30 currencies against MAD.
  class BAM < Base
    URL = "https://api.centralbankofmorocco.ma/cours/Version1/api/CoursVirement"
    EARLIEST_DATE = Date.new(2016, 1, 4)

    class << self
      def key = "BAM"
      def name = "Bank Al-Maghrib"
      def earliest_date = EARLIEST_DATE

      def api_key? = true
      def api_key = ENV["BAM_API_KEY"]

      def backfill(range: 30)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today
      @dataset = []

      first = true
      (start_date..end_date).each do |date|
        next if date.saturday? || date.sunday?

        sleep(15) unless first
        first = false

        @dataset.concat(fetch_date(date))
      end

      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError
      @dataset ||= []
      self
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

        { provider: key, date:, base: code, quote: "MAD", rate: moyen / unite }
      rescue ArgumentError, TypeError
        nil
      end
    end

    private

    def fetch_date(date)
      uri = URI(URL)
      uri.query = URI.encode_www_form(date: "#{date.strftime("%Y-%m-%d")}T12:30:00")
      request = Net::HTTP::Get.new(uri)
      request["Ocp-Apim-Subscription-Key"] = api_key
      response = Net::HTTP.start(uri.hostname, uri.port, use_ssl: true) { |http| http.request(request) }
      parse(response.body)
    rescue Oj::ParseError
      []
    end
  end
end
