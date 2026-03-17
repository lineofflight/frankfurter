# frozen_string_literal: true

require "net/http"
require "oj"

require "providers/base"

module Providers
  # National Bank of Ukraine. Publishes daily rates for ~45 currencies against UAH.
  class NBU < Base
    URL = URI("https://bank.gov.ua/NBU_Exchange/exchange_site")
    EARLIEST_DATE = Date.new(1999, 1, 4)

    def key = "NBU"
    def name = "National Bank of Ukraine"
    def base = "UAH"

    def current
      @dataset = fetch(Date.today - 7, Date.today)
      last_date = @dataset.last&.dig(:date)
      @dataset = @dataset.select { |r| r[:date] == last_date }
      self
    end

    def historical(start_date: EARLIEST_DATE, end_date: Date.today)
      @dataset = fetch(Date.parse(start_date.to_s), Date.parse(end_date.to_s))
      self
    end

    def parse(json)
      data = json.is_a?(String) ? Oj.load(json) : json

      data.filter_map do |row|
        date = Date.strptime(row.fetch("exchangedate"), "%d.%m.%Y")
        next if date.saturday? || date.sunday?

        quote = row.fetch("cc")
        units = row.fetch("units", 1).to_f
        rate = row.fetch("rate").to_f
        next if rate.zero? || units.zero?

        { provider: key, date:, base:, quote:, rate: rate / units }
      end
    end

    private

    def fetch(start_date, end_date)
      url = URL.dup
      url.query = URI.encode_www_form(
        start: start_date.strftime("%Y%m%d"),
        end: end_date.strftime("%Y%m%d"),
        sort: "exchangedate",
        order: "asc",
        json: "",
      )

      parse(Net::HTTP.get(url))
    end
  end
end
