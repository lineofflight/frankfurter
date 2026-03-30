# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Bank of Jamaica daily counter rates in JMD.
  # Fetches from a WPDataTables endpoint (table_id=134), which requires a nonce extracted from the page HTML.
  class BOJA < Base
    PAGE_URL = "https://boj.org.jm/market/foreign-exchange/counter-rates/"
    BASE_URL = "https://boj.org.jm/wp-admin/admin-ajax.php"
    TABLE_ID = 134
    EARLIEST_DATE = Date.new(2006, 1, 3)

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

    class << self
      def key = "BOJA"
      def name = "Bank of Jamaica"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      @dataset = fetch_table

      @dataset = dataset.select { |r| r[:date] >= since } if since
      @dataset = dataset.select { |r| r[:date] <= upto } if upto

      self
    end

    def parse(json)
      data = json.is_a?(String) ? JSON.parse(json) : json
      rows = data["data"]
      return [] unless rows.is_a?(Array)

      rows.filter_map do |row|
        parse_row(row)
      end
    end

    private

    def fetch_table
      nonce = fetch_nonce
      return [] unless nonce

      uri = URI("#{BASE_URL}?action=get_wdtable&table_id=#{TABLE_ID}")
      request = Net::HTTP::Post.new(uri)
      request.set_form_data("draw" => "1", "start" => "0", "length" => "-1", "wdtNonce" => nonce)

      response = Net::HTTP.start(uri.hostname, uri.port, use_ssl: true) do |http|
        http.request(request)
      end

      parse(response.body)
    rescue JSON::ParserError, Net::OpenTimeout, Net::ReadTimeout, Errno::ECONNRESET
      []
    end

    def fetch_nonce
      uri = URI(PAGE_URL)
      response = Net::HTTP.get(uri)
      match = response.match(NONCE_PATTERN)
      match&.captures&.first
    rescue Net::OpenTimeout, Net::ReadTimeout, Errno::ECONNRESET
      nil
    end

    def parse_row(row)
      date_str, currency_name, sell_rate, buy_rate, = row
      return unless date_str && currency_name

      date = Date.strptime(date_str.strip, "%d %b %Y")
      iso = resolve_currency(currency_name.strip)
      return unless iso

      rate_value = compute_mid(sell_rate, buy_rate)
      return unless rate_value && !rate_value.zero?

      { provider: key, date:, base: iso, quote: "JMD", rate: rate_value }
    rescue ArgumentError, TypeError
      nil
    end

    def compute_mid(sell, buy)
      s = Float(sell)
      b = Float(buy)
      return s if b.zero?

      (s + b) / 2.0
    rescue ArgumentError, TypeError
      nil
    end

    def resolve_currency(name)
      CURRENCY_MAP[name.upcase]
    end
  end
end
