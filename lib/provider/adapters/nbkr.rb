# frozen_string_literal: true

require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of the Kyrgyz Republic. Publishes daily rates for 5 major currencies
    # (USD, EUR, RUB, KZT, CNY) and weekly rates for ~35 others against KGS.
    #
    # Two-track adapter:
    #
    # 1. Live XML feed (daily.xml + weekly.xml) for the current snapshot. Each request
    #    returns only the latest published rates, with no date parameter. XML is
    #    Windows-1251 encoded with comma decimal separators. Values are normalized by
    #    Nominal (some weekly currencies are quoted per 10 or 100 units).
    #
    # 2. Historical HTML scrape (index1.jsp?item=1562) for archive data back to
    #    1999-01-01. The historical page exposes a per-currency time series keyed by
    #    NBKR's internal valuta_id. Each request returns up to ~366 (date, value) rows
    #    for one currency across the requested window. The HTML response is UTF-8 with
    #    comma decimal separators; nominals are not in the response, so we carry them
    #    in CURRENCIES alongside the ISO mapping derived from the landing page's
    #    <select> options.
    #
    # The fetch() dispatcher uses the XML feed for "live" windows (upto unset or in
    # the future) and the HTML scrape for "historical" windows (upto strictly in the
    # past). Provider#backfill's incremental loop walks history forward via
    # fetch_each, so a fresh setup drains the archive in 365-day chunks before
    # switching to the live feed once it catches up to today.
    #
    # Records are returned in NBKR's native direction: foreign currency as base, KGS
    # as quote (1 unit foreign = X KGS), matching the convention used by other
    # pivot-in-quote adapters (NBG, CBR, BBK).
    class NBKR < Adapter
      DAILY_URL = "https://www.nbkr.kg/XML/daily.xml"
      WEEKLY_URL = "https://www.nbkr.kg/XML/weekly.xml"
      HISTORICAL_URL = "https://www.nbkr.kg/index1.jsp"
      HISTORICAL_ROW = /<!--date-->(\d{2}\.\d{2}\.\d{4})<!--date-->.*?<!--value-->(\d+(?:,\d+)?)<!--value-->/m

      # NBKR's historical page identifies each currency by an internal valuta_id and
      # publishes rates per Nominal units (foreign side of "Nominal foreign = X KGS").
      # The mapping was extracted from the <select name="valuta_id"> on the landing
      # page and cross-checked against an allVals snapshot to confirm ISO codes.
      # Some currencies appear twice across a redenomination (e.g. BYR/BYN, RUR/RUB,
      # AZM/AZN, TRL/TRY) — each id covers its own slice of history.
      CURRENCIES = [
        { id: 15,  iso: "USD", nominal: 1 },
        { id: 56,  iso: "AUD", nominal: 1 },
        { id: 16,  iso: "ATS", nominal: 1 },
        { id: 82,  iso: "AZN", nominal: 1 },
        { id: 37,  iso: "AZM", nominal: 1000 },
        { id: 17,  iso: "GBP", nominal: 1 },
        { id: 38,  iso: "AMD", nominal: 10 },
        { id: 99,  iso: "AFN", nominal: 1 },
        { id: 39,  iso: "BYR", nominal: 100 },
        { id: 18,  iso: "BEF", nominal: 10 },
        { id: 100, iso: "BGN", nominal: 1 },
        { id: 101, iso: "BRL", nominal: 1 },
        { id: 51,  iso: "HUF", nominal: 10 },
        { id: 25,  iso: "KRW", nominal: 1 },
        { id: 102, iso: "GEL", nominal: 1 },
        { id: 19,  iso: "DKK", nominal: 1 },
        { id: 103, iso: "AED", nominal: 1 },
        { id: 20,  iso: "EUR", nominal: 1 },
        { id: 21,  iso: "INR", nominal: 1 },
        { id: 104, iso: "IRR", nominal: 10 },
        { id: 22,  iso: "ITL", nominal: 100 },
        { id: 40,  iso: "KZT", nominal: 1 },
        { id: 23,  iso: "CAD", nominal: 1 },
        { id: 24,  iso: "CNY", nominal: 1 },
        { id: 50,  iso: "KWD", nominal: 1 },
        { id: 41,  iso: "LVL", nominal: 1 },
        { id: 42,  iso: "LTL", nominal: 1 },
        { id: 105, iso: "MYR", nominal: 1 },
        { id: 43,  iso: "MDL", nominal: 1 },
        { id: 106, iso: "MNT", nominal: 1 },
        { id: 26,  iso: "DEM", nominal: 1 },
        { id: 27,  iso: "NLG", nominal: 1 },
        { id: 57,  iso: "TRY", nominal: 1 },
        { id: 53,  iso: "NZD", nominal: 1 },
        { id: 107, iso: "TWD", nominal: 1 },
        { id: 108, iso: "TMT", nominal: 1 },
        { id: 28,  iso: "NOK", nominal: 1 },
        { id: 55,  iso: "PKR", nominal: 1 },
        { id: 109, iso: "PLN", nominal: 1 },
        { id: 29,  iso: "PTE", nominal: 1 },
        { id: 44,  iso: "RUB", nominal: 1 },
        { id: 98,  iso: "RUR", nominal: 1000 },
        { id: 30,  iso: "XDR", nominal: 1 },
        { id: 86,  iso: "SGD", nominal: 1 },
        { id: 45,  iso: "TJS", nominal: 1 },
        { id: 49,  iso: "TJR", nominal: 100 },
        { id: 31,  iso: "TRL", nominal: 1000 },
        { id: 46,  iso: "UZS", nominal: 1 },
        { id: 47,  iso: "UAH", nominal: 1 },
        { id: 32,  iso: "FIM", nominal: 1 },
        { id: 33,  iso: "FRF", nominal: 1 },
        { id: 52,  iso: "CZK", nominal: 1 },
        { id: 34,  iso: "SEK", nominal: 1 },
        { id: 35,  iso: "CHF", nominal: 1 },
        { id: 48,  iso: "EEK", nominal: 1 },
        { id: 36,  iso: "JPY", nominal: 10 },
        { id: 139, iso: "SAR", nominal: 1 },
        { id: 160, iso: "BYN", nominal: 1 },
        { id: 180, iso: "OMR", nominal: 1 },
        { id: 182, iso: "HKD", nominal: 1 },
        { id: 184, iso: "IDR", nominal: 10 },
      ].freeze

      class << self
        # Per-currency historical pages return up to ~366 rows. One year per chunk
        # keeps each request bounded and means a full backfill from 1999 is a
        # series of single-year, per-currency fetches.
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        records = if historical?(after, upto)
          fetch_historical(after, upto)
        else
          fetch_live
        end

        records.select! { |r| r[:date] >= after } if after
        records.select! { |r| r[:date] <= upto } if upto
        records
      end

      def parse(xml)
        xml = xml.dup.force_encoding(Encoding::WINDOWS_1251)
          .encode(Encoding::UTF_8, invalid: :replace, undef: :replace, replace: "")
        root = Ox.load(xml).locate("CurrencyRates").first
        raise "NBKR: CurrencyRates root missing from XML feed" unless root

        date_attr = root[:Date]
        raise "NBKR: Date attribute missing from CurrencyRates feed" unless date_attr

        date = Date.strptime(date_attr, "%d.%m.%Y")

        root.locate("Currency").filter_map do |node|
          code = node[:ISOCode]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          nominal = node.locate("Nominal").first&.text.to_i
          next if nominal.zero?

          value = node.locate("Value").first&.text
          next if value.nil? || value.empty?

          rate = Float(value.tr(",", "."), exception: false)
          next unless rate&.positive?

          { date:, base: code, quote: "KGS", rate: rate / nominal }
        end
      end

      # Parses the per-currency historical HTML series. The page interleaves comment
      # markers around each cell (<!--date-->...<!--date-->, <!--value-->...
      # <!--value-->) which we match pairwise.
      def parse_historical(html, iso:, nominal:)
        html.scan(HISTORICAL_ROW).filter_map do |date_str, value_str|
          rate = Float(value_str.tr(",", "."), exception: false)
          next unless rate&.positive?

          date = Date.strptime(date_str, "%d.%m.%Y")
          { date:, base: iso, quote: "KGS", rate: rate / nominal }
        end
      end

      private

      def historical?(after, upto)
        # The historical scrape needs both endpoints of the window. Live XML covers
        # the unbounded "catch up to today" case (upto unset or in the future); any
        # bounded window strictly in the past goes to the HTML scrape.
        return false if after.nil? || upto.nil?

        upto < Date.today
      end

      def fetch_live
        parse(http.get(DAILY_URL).to_s) + parse(http.get(WEEKLY_URL).to_s)
      end

      def fetch_historical(after, upto)
        dataset = []

        # NBKR's endpoint starts dropping connections after ~20 fresh TLS sessions in quick succession, so this loop
        # reuses one persistent connection across all ~60 per-currency requests instead of opening a new one each time.
        # The sleep between requests adds further pacing on top of that.
        http.persistent(HISTORICAL_URL) do |client|
          first = true
          CURRENCIES.each do |currency|
            sleep(0.5) unless first
            first = false

            html = fetch_historical_currency(client, currency[:id], after, upto)
            dataset.concat(parse_historical(html, iso: currency[:iso], nominal: currency[:nominal]))
          end
        end

        dataset
      end

      def fetch_historical_currency(client, id, after, upto)
        client.get(HISTORICAL_URL, params: {
          item: 1562,
          lang: "ENG",
          valuta_id: id,
          beg_day: after.strftime("%d"),
          beg_month: after.strftime("%m"),
          beg_year: after.strftime("%Y"),
          end_day: upto.strftime("%d"),
          end_month: upto.strftime("%m"),
          end_year: upto.strftime("%Y"),
        }).to_s
      end
    end
  end
end
