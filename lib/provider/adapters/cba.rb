# frozen_string_literal: true

require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Armenia. Publishes daily rates for ~30 currencies against AMD, and has published about 70 since
    # 2000.
    class CBA < Adapter
      URL = URI("https://api.cba.am/exchangerates.asmx")
      CHUNK_SIZE = 365
      TROY_OUNCE_GRAMS = 31.1035
      PRECIOUS_METALS = ["XAU", "XAG"].freeze

      # The range endpoint returns only the codes it is asked for, and the latest bulletin lists only what CBA still
      # quotes, so the series it has since dropped are requested by name: the legacy euro currencies, the litas, lats,
      # kroon and koruna, and the currencies it stopped quoting after 2022-02-28. Left out are TAD and TMM, which copy
      # the TJS and TMT series through 2000, and the TMT, TRL, ROL and RON series, whose values stray from other sources
      # by 2x to 500x for months or years at a time.
      DROPPED_CODES = [
        "ARP", "ARS", "ATS", "BEF", "BGL", "BGN", "BRC", "BYR", "DEM", "DKK", "EEK", "EGP", "ESP", "FIM", "FRF", "GRD",
        "HUF", "IEP", "ILS", "ISK", "ITL", "KRW", "KWD", "LBP", "LTL", "LVL", "MDL", "MXN", "NLG", "PLZ", "PTE", "SAR",
        "SDR", "SKK", "SYP", "TRY", "USM",
      ].freeze

      # Labels CBA uses for a current currency: retired codes for the Argentine peso, lev, real and zloty (its history
      # starts in 2000, after each of them was redenominated), SDR for the XDR, and USM for the Uzbek som. Each hands
      # over to the right code without a break: 1 "PLZ" = 124.89 AMD on 2006-12-30, 1 PLN = 125.18 on 2007-01-05.
      ALIASES = {
        "ARP" => "ARS",
        "BGL" => "BGN",
        "BRC" => "BRL",
        "PLZ" => "PLN",
        "SDR" => "XDR",
        "USM" => "UZS",
      }.freeze

      # CBA's TJS series holds the Tajik ruble until the somoni takes over: 10 "TJS" = 26.71 AMD on 2000-10-30, and the
      # somoni at 1 TJS = 250.74 AMD on 2000-11-01.
      PREDECESSORS = { "TJS" => ["TJR", Date.new(2000, 11, 1)] }.freeze

      # Series whose amount field understates the quote tenfold, keyed by label with the first date it is right: 1 KZT =
      # 37.37 AMD on 2004-12-30, 10 KZT = 37.39 AMD on 2005-01-04, and 1 ISK = 35.40 AMD on 2015-03-06, 10 ISK = 35.12
      # AMD on 2015-03-09. USM is per 10 som throughout, 1 "USM" = 2.93 AMD on 2006-12-30 against 10 UZS = 2.94 AMD on
      # 2007-01-05, and so is the Tajik ruble under TJS, which otherwise comes out at ten times NBU's and CBR's rates.
      UNDERSTATED_AMOUNTS = {
        "ISK" => Date.new(2015, 3, 9),
        "KZT" => Date.new(2005, 1, 4),
        "TJS" => Date.new(2000, 11, 1),
        "USM" => Date.new(2007, 1, 5),
      }.freeze

      class << self
        # A full backfill stores about 300,000 rows. Yearly windows keep each insert, and the blend refresh that follows
        # it, to one year instead of holding the write lock for the whole history.
        def backfill_range = CHUNK_SIZE
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        iso_codes = (current_currency_codes | DROPPED_CODES).join(",")
        records = []
        chunk_start = after

        while chunk_start <= end_date
          chunk_end = [chunk_start + CHUNK_SIZE - 1, end_date].min
          records.concat(range(chunk_start, chunk_end, iso_codes))
          chunk_start = chunk_end + 1
        end

        # SDR and XDR overlap in early 2017 with equal values.
        records.uniq { |record| record.values_at(:date, :base, :quote) }
      end

      private

      def current_currency_codes
        response = request("ExchangeRatesLatest", <<~XML)
          <ExchangeRatesLatest xmlns="http://www.cba.am/" />
        XML

        result = response.locate("soap:Envelope/soap:Body/ExchangeRatesLatestResponse/ExchangeRatesLatestResult").first
        return [] unless result

        result.locate("Rates/ExchangeRate").filter_map do |node|
          node.locate("ISO").first&.text
        end
      end

      def range(start_date, end_date, iso_codes)
        response = request("ExchangeRatesByDateRangeByISO", <<~XML)
          <ExchangeRatesByDateRangeByISO xmlns="http://www.cba.am/">
            <ISOCodes>#{iso_codes}</ISOCodes>
            <DateFrom>#{start_date}</DateFrom>
            <DateTo>#{end_date}</DateTo>
          </ExchangeRatesByDateRangeByISO>
        XML

        path = "soap:Envelope/soap:Body/ExchangeRatesByDateRangeByISOResponse/" \
               "ExchangeRatesByDateRangeByISOResult/diffgr:diffgram/DocumentElement/ExchangeRatesByRange"
        response
          .locate(path)
          .filter_map do |row|
            iso = row.locate("ISO").first&.text
            next unless iso

            date = Date.parse(row.locate("RateDate").first.text)
            base = historical_code(ALIASES.fetch(iso, iso), date)
            { date:, base:, quote: "AMD", rate: extract_rate(row, iso, date) }
          end
      end

      def request(action, payload)
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                         xmlns:xsd="http://www.w3.org/2001/XMLSchema"
                         xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              #{payload.strip}
            </soap:Body>
          </soap:Envelope>
        XML

        headers = {
          "Content-Type" => "text/xml; charset=utf-8",
          "SOAPAction" => "\"http://www.cba.am/#{action}\"",
        }
        response = http.post(URL, body: xml, headers:)

        Ox.load(response.to_s)
      end

      def extract_rate(node, iso, date)
        amount = Integer(node.locate("Amount").first.text)
        amount *= 10 if (corrected = UNDERSTATED_AMOUNTS[iso]) && date < corrected
        rate = Float(node.locate("Rate").first.text)
        rate *= TROY_OUNCE_GRAMS if PRECIOUS_METALS.include?(iso)
        rate / amount
      end
    end
  end
end
