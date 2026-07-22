# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Jamaica daily counter rates in JMD.
    # Fetches from a WPDataTables endpoint (table_id=134), which requires a nonce extracted from the page HTML.
    class BOJA < Adapter
      PAGE_URL = "https://boj.org.jm/market/foreign-exchange/counter-rates/"
      BASE_URL = "https://boj.org.jm/wp-admin/admin-ajax.php"
      TABLE_ID = 134
      NONCE_PATTERN = /wdtNonceFrontendServerSide_#{TABLE_ID}"\s+value="([^"]+)"/

      CURRENCY_MAP = {
        "U.S. DOLLAR" => "USD",
        "GREAT BRITAIN POUND" => "GBP",
        "CANADA DOLLAR" => "CAD",
        "EURO" => "EUR",
        "JAPANESE YEN" => "JPY",
        "SWISS FRANC" => "CHF",
        "AUSTRALIAN DOLLAR" => "AUD",
        "DANISH KRONE" => "DKK",
        "NORWEGIAN KRONE" => "NOK",
        "SWEDISH KRONA" => "SEK",
        "HONG KONG DOLLAR" => "HKD",
        "BARBADOS DOLLAR" => "BBD",
        "BELIZE DOLLAR" => "BZD",
        "T&T DOLLAR" => "TTD",
        "BAHAMAS DOLLAR" => "BSD",
        "CAYMAN DOLLAR" => "KYD",
        "GUYANA DOLLAR" => "GYD",
        "E. C. DOLLAR" => "XCD",
        "DOMINICAN REP. PESO" => "DOP",
        "GIBRALTAR POUND" => "GIP",
        "NORTHERN IRELAND POUND" => "GBP",
      }.freeze

      def fetch(after: nil, upto: nil)
        dataset = fetch_table

        dataset = dataset.select { |r| r[:date] >= after } if after
        dataset = dataset.select { |r| r[:date] <= upto } if upto

        dataset
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        rows = data["data"]
        raise "BOJA: data array missing from WPDataTables response" unless rows.is_a?(Array)

        rows.filter_map do |row|
          parse_row(row)
        end
      end

      private

      def fetch_table
        nonce = fetch_nonce
        raise "BOJA: wdtNonce not found on counter-rates page" unless nonce

        uri = "#{BASE_URL}?action=get_wdtable&table_id=#{TABLE_ID}"
        form = { "draw" => "1", "start" => "0", "length" => "-1", "wdtNonce" => nonce }

        parse(http.post(uri, form:).to_s)
      end

      def fetch_nonce
        body = http.get(PAGE_URL).to_s
        match = body.match(NONCE_PATTERN)
        match&.captures&.first
      end

      def parse_row(row)
        date_str, currency_name, sell_rate, buy_rate, = row
        return unless date_str && currency_name

        date = Date.strptime(date_str.strip, "%d %b %Y")
        iso = resolve_currency(currency_name.strip)
        return unless iso

        rate_value = compute_mid(sell_rate, buy_rate)
        return unless rate_value && !rate_value.zero?

        { date:, base: iso, quote: "JMD", rate: rate_value }
      end

      def compute_mid(sell, buy)
        s = Float(sell)
        b = Float(buy)
        return s if b.zero?

        (s + b) / 2.0
      end

      def resolve_currency(name)
        CURRENCY_MAP[name.upcase]
      end
    end
  end
end
