# frozen_string_literal: true

require "net/http"
require "oj"

require "providers/base"

module Providers
  # Czech National Bank. Publishes daily exchange rates for 30 currencies
  # against the Czech koruna (CZK) via a REST JSON API.
  class CNB < Base
    URL = "https://api.cnb.cz/cnbapi/exrates/daily"
    EARLIEST_DATE = Date.new(1991, 1, 1)

    class << self
      def key = "CNB"
      def name = "Czech National Bank"
      def earliest_date = EARLIEST_DATE

      def backfill(range: 30)
        super
      end
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      end_date = upto || Date.today
      @dataset = []

      first = true
      (start_date..end_date).each do |date|
        next if date.saturday? || date.sunday?

        sleep(0.2) unless first
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
      end
    end

    private

    def fetch_date(date)
      url = URI(URL)
      url.query = URI.encode_www_form(date: date.strftime("%Y-%m-%d"), lang: "EN")
      response = Net::HTTP.get(url)
      parse(response)
    rescue Oj::ParseError
      []
    end
  end
end
