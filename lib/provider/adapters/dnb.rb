# frozen_string_literal: true

require "csv"
require "net/http"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Danmarks Nationalbank. Publishes daily exchange rates for 30 currencies
    # against the Danish krone (DKK) via Statistics Denmark's Statbank API.
    # Rates are quoted as DKK per 100 units of foreign currency.
    class DNB < Adapter
      URL = "https://api.statbank.dk/v1/data"
      CURRENCIES = [
        "EUR",
        "USD",
        "GBP",
        "SEK",
        "NOK",
        "CHF",
        "CAD",
        "JPY",
        "AUD",
        "NZD",
        "PLN",
        "CZK",
        "HUF",
        "HKD",
        "SGD",
        "ZAR",
        "BGN",
        "RON",
        "TRY",
        "KRW",
        "THB",
        "MYR",
        "PHP",
        "IDR",
        "CNY",
        "BRL",
        "MXN",
        "INR",
        "ILS",
        "ISK",
      ].freeze

      def fetch(after: nil, upto: nil)
        tid = ">=#{format_date(after)}"
        tid += "<=#{format_date(upto || Date.today)}" if upto

        body = Oj.dump(
          {
            table: "DNVALD",
            format: "BULK",
            lang: "en",
            valuePresentation: "Code",
            variables: [
              { code: "VALUTA", values: CURRENCIES },
              { code: "KURTYP", values: ["KBH"] },
              { code: "Tid", values: [tid] },
            ],
          },
          mode: :compat,
        )

        uri = URI(URL)
        response = Net::HTTP.post(uri, body, "Content-Type" => "application/json")
        parse(response.body)
      end

      def parse(csv)
        csv = csv.encode("UTF-8", "UTF-8", invalid: :replace, undef: :replace)
        csv = csv.sub("\xEF\xBB\xBF", "")
        rows = CSV.parse(csv, col_sep: ";", headers: true)

        rows.filter_map do |row|
          code = row["VALUTA"]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          rate_str = row["INDHOLD"]
          next if rate_str.nil? || rate_str.strip == ".."

          rate = Float(rate_str)
          next if rate.zero?

          tid = row["TID"]
          next unless tid

          date = parse_date(tid)
          { date:, base: code, quote: "DKK", rate: rate / 100.0 }
        end
      end

      private

      def format_date(date)
        date.strftime("%YM%mD%d")
      end

      def parse_date(str)
        Date.new(str[0, 4].to_i, str[5, 2].to_i, str[8, 2].to_i)
      end
    end
  end
end
