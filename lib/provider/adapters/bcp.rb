# frozen_string_literal: true

require "net/http"
require "nokogiri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central del Paraguay (BCP). Publishes the "tipo de cambio referencial
    # interbancario", a weighted average of interbank spot operations, on business
    # days against the Paraguayan guaraní (PYG). The historical endpoint returns
    # a 12-month x 31-day matrix per (year, currency); ND cells mark non-trading
    # days. Rates are PYG-per-foreign — pivot PYG goes in quote, foreign in base.
    # XAU is published per troy ounce already.
    class BCP < Adapter
      BASE_URL = "https://www.bcp.gov.py/webapps/web/cotizacion/monedas-historica"
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"

      # ISO 4217 → BCP's URL label, for currencies BCP serves under a
      # non-ISO name. XDR (Special Drawing Rights) is published at
      # ?moneda=SDR.
      URL_ALIASES = { "XDR" => "SDR" }.freeze

      # Quote currencies on the daily snapshot. Currencies have varying
      # historical depth — USD/EUR back to 2001, most others from ~2012, some
      # later — so empty cells are expected on older years.
      CURRENCIES = [
        "USD",
        "EUR",
        "GBP",
        "JPY",
        "CHF",
        "CAD",
        "AUD",
        "CNY",
        "BRL",
        "ARS",
        "CLP",
        "MXN",
        "UYU",
        "COP",
        "BOB",
        "NZD",
        "ZAR",
        "SEK",
        "DKK",
        "NOK",
        "AED",
        "PEN",
        "SGD",
        "TWD",
        "XAU",
        "XDR",
      ].freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        years = (after&.year || end_date.year)..end_date.year

        records = []
        first = true
        years.each do |year|
          CURRENCIES.each do |currency|
            sleep(0.5) unless first
            first = false

            records.concat(fetch_year(year, currency))
          end
        end

        records.select { |r| (after.nil? || r[:date] > after) && r[:date] <= end_date }
      end

      def parse(html, year:, currency:)
        doc = Nokogiri::HTML.parse(html)
        records = []

        doc.css("tbody tr").each do |row|
          day_text = row.at_css("th")&.text&.strip
          next unless day_text&.match?(/\A\d{1,2}\z/)

          day = day_text.to_i
          cells = row.css("td")
          next if cells.length < 12

          cells.first(12).each_with_index do |cell, index|
            month = index + 1
            value = parse_value(cell.text)
            next unless value

            date = safe_date(year, month, day)
            next unless date

            records << { date:, base: currency, quote: "PYG", rate: value }
          end
        end

        records
      end

      private

      def fetch_year(year, currency)
        url = URI(BASE_URL)
        url.query = URI.encode_www_form(anho: year, moneda: URL_ALIASES.fetch(currency, currency))
        req = Net::HTTP::Get.new(url)
        req["User-Agent"] = USER_AGENT

        http = Net::HTTP.new(url.host, url.port)
        http.use_ssl = true
        http.open_timeout = 30
        http.read_timeout = 60

        response = http.request(req)
        response.value

        parse(response.body, year:, currency:)
      end

      def parse_value(cell)
        text = cell.strip
        return if text.empty? || text == "ND"

        normalized = text.delete(".").tr(",", ".")
        value = Float(normalized, exception: false)
        return unless value&.positive?

        value
      end

      def safe_date(year, month, day)
        Date.new(year, month, day)
      rescue Date::Error
        nil
      end
    end
  end
end
