# frozen_string_literal: true

require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # International Monetary Fund representative exchange rates.
    # Daily rates for 50+ currencies. Most rates are foreign currency per USD;
    # currencies marked with (1) are USD per foreign unit.
    class IMF < Adapter
      BASE_URL = "https://www.imf.org/external/np/fin/data/rms_mth.aspx"

      # Currency name to ISO code mapping
      CURRENCY_MAP = {
        "afghan afghani" => "AFN",
        "algerian dinar" => "DZD",
        "angolan kwanza" => "AOA",
        "australian dollar" => "AUD",
        "bahamian dollar" => "BSD",
        "bahrain dinar" => "BHD",
        "bangladeshi taka" => "BDT",
        "barbados dollar" => "BBD",
        "botswana pula" => "BWP",
        "brazilian real" => "BRL",
        "brunei dollar" => "BND",
        "canadian dollar" => "CAD",
        "chilean peso" => "CLP",
        "chinese yuan" => "CNY",
        "colombian peso" => "COP",
        "costa rican colon" => "CRC",
        "czech koruna" => "CZK",
        "danish krone" => "DKK",
        "djibouti franc" => "DJF",
        "egyptian pound" => "EGP",
        "euro" => "EUR",
        "fiji dollar" => "FJD",
        "hungarian forint" => "HUF",
        "icelandic krona" => "ISK",
        "indian rupee" => "INR",
        "iranian rial" => "IRR",
        "israeli new shekel" => "ILS",
        "japanese yen" => "JPY",
        "jordanian dinar" => "JOD",
        "kazakhstani tenge" => "KZT",
        "kuwaiti dinar" => "KWD",
        "libyan dinar" => "LYD",
        "malaysian ringgit" => "MYR",
        "mauritian rupee" => "MUR",
        "mexican peso" => "MXN",
        "moldovan leu" => "MDL",
        "mongolian tugrik" => "MNT",
        "moroccan dirham" => "MAD",
        "mozambican metical" => "MZN",
        "myanmar kyat" => "MMK",
        "namibian dollar" => "NAD",
        "nepalese rupee" => "NPR",
        "new zealand dollar" => "NZD",
        "nigerian naira" => "NGN",
        "norwegian krone" => "NOK",
        "omani rial" => "OMR",
        "pakistani rupee" => "PKR",
        "peruvian sol" => "PEN",
        "philippine peso" => "PHP",
        "polish zloty" => "PLN",
        "qatari riyal" => "QAR",
        "romanian leu" => "RON",
        "russian ruble" => "RUB",
        "rwandan franc" => "RWF",
        "samoan tala" => "WST",
        "saudi arabian riyal" => "SAR",
        "singapore dollar" => "SGD",
        "south african rand" => "ZAR",
        "korean won" => "KRW",
        "sri lankan rupee" => "LKR",
        "swedish krona" => "SEK",
        "swiss franc" => "CHF",
        "thai baht" => "THB",
        "trinidadian dollar" => "TTD",
        "tunisian dinar" => "TND",
        "ugandan shilling" => "UGX",
        "u.a.e. dirham" => "AED",
        "u.k. pound" => "GBP",
        "u.s. dollar" => "USD",
        "uruguayan peso" => "UYU",
        "venezuelan bolivar" => "VES",
        "yemeni rial" => "YER",
        "zambian kwacha" => "ZMW",
      }.freeze

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today

        dataset = []
        cursor = Date.new(after.year, after.month, 1)

        while cursor <= end_date
          last_day = Date.new(cursor.year, cursor.month, -1)
          dataset.concat(parse(fetch_month(last_day, "REP")))
          dataset.concat(parse_sdrcv(fetch_month(last_day, "SDRCV")))
          cursor >>= 1
        end

        dataset = dataset.select { |r| r[:date] >= after } if after
        dataset
      end

      def parse(tsv)
        parse_rows(tsv) do |currency_name, rate, date|
          indirect = currency_name.end_with?("(1)")
          clean_name = currency_name.delete_suffix("(1)").strip.downcase
          iso = CURRENCY_MAP[clean_name]
          next unless iso
          next if iso == "USD"

          if indirect
            { date:, base: iso, quote: "USD", rate: }
          else
            { date:, base: "USD", quote: iso, rate: }
          end
        end
      end

      # SDR cross rates: "SDRs per Currency unit" — natively base=currency, quote=XDR.
      def parse_sdrcv(tsv)
        parse_rows(tsv) do |currency_name, rate, date|
          iso = CURRENCY_MAP[currency_name.downcase]
          next unless iso

          { date:, base: iso, quote: "XDR", rate: }
        end
      end

      private

      def parse_rows(tsv)
        return [] if tsv.nil? || tsv.strip.empty?

        records = []
        dates = nil

        tsv.each_line(chomp: true) do |line|
          next if line.strip.empty?

          if line.start_with?("Currency\t")
            headers = line.split("\t")
            dates = headers[1..].map { |h| Date.parse(h.strip) }
            next
          end

          next unless dates

          cols = line.split("\t")
          currency_name = cols[0]&.strip
          next unless currency_name

          cols[1..].zip(dates).each do |value, date|
            next unless date

            cleaned = value&.tr(",", "")&.gsub(/[^0-9.\-]/, "")
            next if cleaned.nil? || cleaned.empty?

            rate = Float(cleaned)
            next if rate.zero?

            record = yield(currency_name, rate, date)
            records << record if record
          end
        end

        records
      end

      def fetch_month(last_day, report_type)
        url = URI(BASE_URL)
        url.query = URI.encode_www_form(
          SelectDate: last_day.to_s,
          reportType: report_type,
          tsvflag: "Y",
        )

        Net::HTTP.get(url)
      end
    end
  end
end
