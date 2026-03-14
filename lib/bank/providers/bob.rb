# frozen_string_literal: true

require "date"
require "net/http"

require "bank/provider"

module Bank
  module Providers
    class BOB < Provider
      CSV_URL = URI("https://www.bankofbotswana.bw/export/exchange-rates.csv?page&_format=csv")

      def current
        historical.last(1)
      end

      def ninety_days
        cutoff = Date.today - 120
        historical.select { |day| day[:date] >= cutoff }
      end

      def historical
        parse(Net::HTTP.get(CSV_URL))
      end

      def saved_data
        []
      end

      def supported_currencies
        ["BWP"]
      end

      private

      def parse(csv)
        rows = csv.delete_prefix("\uFEFF").lines(chomp: true)
        headers = rows.shift&.split(",")
        eur_index = headers&.index("EUR")
        date_index = headers&.index("Date")
        return [] unless eur_index && date_index

        rows.filter_map do |line|
          values = line.delete('"').split(",")
          eur = values[eur_index]
          next if eur.nil? || eur.empty?

          rate = Float(eur)
          next if rate.zero?

          {
            date: Date.strptime(values[date_index], "%d %b %Y"),
            rates: { "BWP" => 1 / rate },
          }
        end.reverse
      end
    end
  end
end
