# frozen_string_literal: true

require "net/http"
require "oj"

require "providers/base"

module Providers
  # Czech National Bank. Publishes daily exchange rates for 30 currencies
  # against the Czech koruna (CZK) via a REST JSON API.
  class CNB < Base
    URL = "https://api.cnb.cz/cnbapi/exrates/daily-year"
    EARLIEST_DATE = Date.new(1991, 1, 1)

    class << self
      def key = "CNB"
      def name = "Czech National Bank"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      end_date = upto || Date.today
      @dataset = []

      (start_date.year..end_date.year).each do |year|
        @dataset.concat(fetch_year(year))
      end

      @dataset.select! { |r| r[:date].between?(start_date, end_date) }
      self
    rescue Net::OpenTimeout, Net::ReadTimeout, Socket::ResolutionError
      @dataset ||= []
      self
    end

    def parse(json)
      data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
      rates = data.is_a?(Hash) ? data["rates"] : nil
      return [] unless rates.is_a?(Array) && !rates.empty?

      rates.filter_map do |r|
        code = r["currencyCode"]
        next unless code&.match?(/\A[A-Z]{3}\z/)

        amount = r["amount"].to_f
        rate = r["rate"].to_f
        next if rate.zero? || amount.zero?

        date = Date.parse(r["validFor"])
        { provider: key, date:, base: code, quote: "CZK", rate: rate / amount }
      rescue ArgumentError, TypeError
        nil
      end
    end

    private

    def fetch_year(year)
      url = URI(URL)
      url.query = URI.encode_www_form(year: year, lang: "EN")
      response = Net::HTTP.get(url)
      parse(response)
    rescue Oj::ParseError
      []
    end
  end
end
