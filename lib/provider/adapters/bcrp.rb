# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banco Central de Reserva del Perú. Pulls the end-of-day banking-system (SBS) buy and sell quotes for USD from the
    # BCRPData statistical series API and coerces them to the mid. The interbank series (PD04637PD, PD04638PD) are
    # volume-weighted averages carrying float noise; the SBS series are the published close at three decimals. The whole
    # history comes back in one call, so no chunking.
    #
    # BCRPData also carries an SBS euro pair (PD04647PD, PD04648PD). It is left out: the mid drifts several percent from
    # the cross on ordinary days and, on a handful of days, the USD figure shows up in the euro column (2.81 on
    # 2014-03-28, 2.87 on 2017-08-31).
    class BCRP < Adapter
      API_URL = "https://estadisticas.bcrp.gob.pe/estadisticas/series/api"

      # Currency => [buy series, sell series], soles per unit of currency.
      SERIES = {
        "USD" => ["PD04639PD", "PD04640PD"],
      }.freeze

      # Period labels with the English locale are "04.Set.26": English month abbreviations except September, which stays
      # Spanish.
      MONTHS = Date::ABBR_MONTHNAMES.compact.each_with_index.to_h { |name, i| [name, i + 1] }.merge("Set" => 9).freeze

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(1997, 1, 2)
        end_date = upto || Date.today
        url = "#{API_URL}/#{SERIES.values.flatten.join("-")}/json/#{start_date}/#{end_date}/ing"

        parse(http.get(url).to_s)
      end

      def parse(body)
        JSON.parse(body).fetch("periods").flat_map do |period|
          date = parse_date(period["name"])
          values = period["values"]

          SERIES.each_with_index.filter_map do |(currency, _), i|
            buy, sell = values[2 * i, 2]
            next if buy == "n.d." || sell == "n.d."

            { date:, base: currency, quote: "PEN", rate: midpoint(buy, sell) }
          end
        end
      end

      private

      def parse_date(name)
        day, month, year = name.split(".")

        Date.strptime("#{day}.#{MONTHS.fetch(month)}.#{year}", "%d.%m.%y")
      end
    end
  end
end
