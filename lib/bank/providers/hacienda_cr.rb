# frozen_string_literal: true

require "date"
require "net/http"
require "oj"

require "bank/provider"
require "bank/providers/ecb"

module Bank
  module Providers
    class HaciendaCR < Provider
      DOLLAR_HISTORICAL_URL = URI("https://api.hacienda.go.cr/indicadores/tc/dolar/historico")
      EURO_CURRENT_URL = URI("https://api.hacienda.go.cr/indicadores/tc/euro")

      def initialize(ecb_provider: ECB.new)
        super()
        @ecb_provider = ecb_provider
      end

      def current
        row = Oj.load(Net::HTTP.get(EURO_CURRENT_URL))

        [
          {
            date: Date.parse(row.fetch("fecha")),
            rates: { "CRC" => Float(row.fetch("colones")) },
          },
        ]
      end

      def ninety_days
        append_current_quote(range(Date.today - 120, Date.today, ecb_provider.ninety_days))
      end

      def historical
        append_current_quote(range(Date.new(1999, 1, 4), Date.today, ecb_provider.historical))
      end

      def saved_data
        []
      end

      def supported_currencies
        ["CRC"]
      end

      private

      attr_reader :ecb_provider

      def range(start_date, end_date, eur_quotes)
        usd_per_eur_by_date = eur_quotes.each_with_object({}) do |day, rates|
          usd_per_eur = day[:rates]["USD"]
          next unless usd_per_eur

          rates[day[:date]] = usd_per_eur
        end

        fetch_dollar_history(start_date, end_date).filter_map do |row|
          date = Date.parse(row.fetch("fecha"))
          usd_per_eur = usd_per_eur_by_date[date]
          next unless usd_per_eur

          {
            date: date,
            rates: { "CRC" => Float(row.fetch("venta")) * usd_per_eur },
          }
        end
      end

      def fetch_dollar_history(start_date, end_date)
        url = DOLLAR_HISTORICAL_URL.dup
        url.query = URI.encode_www_form(
          d: start_date.to_s,
          h: end_date.to_s,
        )

        rows = Oj.load(Net::HTTP.get(url))
        return rows if rows.is_a?(Array)
        return rows.fetch("data", []) if rows.is_a?(Hash)

        []
      end

      def append_current_quote(days)
        latest = current.first
        return days unless latest
        return days if days.any? { |day| day[:date] == latest[:date] }

        (days + [latest]).sort_by { |day| day[:date] }
      end
    end
  end
end
