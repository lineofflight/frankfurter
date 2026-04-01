# frozen_string_literal: true

require "json"
require "net/http"

require "providers/base"

module Providers
  # Central Bank of Kenya daily exchange rates in KES.
  # Fetches from two WPDataTables endpoints: table 32 (2003-2024) and table 193 (2024+).
  class CBK < Base
    BASE_URL = "https://www.centralbank.go.ke/wp-admin/admin-ajax.php"
    EARLIEST_DATE = Date.new(2003, 9, 12)

    CURRENCY_MAP = {
      "US DOLLAR" => "USD",
      "STG POUND" => "GBP",
      "EURO" => "EUR",
      "JAPANESE YEN" => "JPY",
      "JPY" => "JPY",
      "SWISS FRANC" => "CHF",
      "S FRANC" => "CHF",
      "CAN DOLLAR" => "CAD",
      "CAN $" => "CAD",
      "AUSTRALIAN DOLLAR" => "AUD",
      "AUSTRALIAN $" => "AUD",
      "INDIAN RUPEE" => "INR",
      "IND RUPEE" => "INR",
      "SWEDISH KRONA" => "SEK",
      "SW KRONER" => "SEK",
      "NORWEGIAN KRONE" => "NOK",
      "NOR KRONER" => "NOK",
      "DAN KRONER" => "DKK",
      "CHINESE YUAN" => "CNY",
      "S. AFRICAN RAND" => "ZAR",
      "SA RAND" => "ZAR",
      "AE DIRHAM" => "AED",
      "UAE DIRHAM" => "AED",
      "HONGKONG DOLLAR" => "HKD",
      "SINGAPORE DOLLAR" => "SGD",
      "SAUDI RIYAL" => "SAR",
      "USHS" => "UGX",
      "TSHS" => "TZS",
      "RWF" => "RWF",
      "BIF" => "BIF",
    }.freeze

    class << self
      def key = "CBK"
      def name = "Central Bank of Kenya"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      @dataset = []

      if since.nil? || since < Date.new(2024, 1, 5)
        @dataset.concat(fetch_table(32))
      end

      @dataset.concat(fetch_table(193))

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

    def fetch_table(table_id)
      uri = URI("#{BASE_URL}?action=get_wdtable&table_id=#{table_id}")
      request = Net::HTTP::Post.new(uri)
      request.set_form_data("draw" => "1", "start" => "0", "length" => "-1")

      response = Net::HTTP.start(uri.hostname, uri.port, use_ssl: true) do |http|
        http.request(request)
      end

      parse(response.body)
    rescue JSON::ParserError, Net::OpenTimeout, Net::ReadTimeout, Errno::ECONNRESET
      []
    end

    def parse_row(row)
      date_str, currency_name, *rest = row
      return unless date_str && currency_name

      date = Date.strptime(date_str.strip, "%d/%m/%Y")
      iso, cross_rate = resolve_currency(currency_name.strip)
      return unless iso

      units = parse_units(currency_name)
      rate_value = Float(rest[0])
      return if rate_value.zero?

      rate_value /= units if units > 1
      rate_value = 1.0 / rate_value if cross_rate

      { provider: key, date:, base: iso, quote: "KES", rate: rate_value }
    rescue ArgumentError, TypeError
      nil
    end

    def resolve_currency(name)
      upname = name.upcase

      # Check for East African cross-rate patterns like "KES / USHS" or "KEN SHILLING / USHS"
      if upname.match?(%r{KE[SN][\s/]|KEN SHILLING\s*/})
        suffix = upname.split("/").last.strip
        iso = CURRENCY_MAP[suffix]
        return [iso, true] if iso
      end

      # Strip unit markers like "(100)" before matching
      clean = upname.sub(/\s*\(\d+\)\s*$/, "")
      iso = CURRENCY_MAP[clean] || CURRENCY_MAP.find { |k, _| clean.include?(k) }&.last
      [iso, false]
    end

    def parse_units(name)
      match = name.match(/\((\d+)\)\s*$/)
      match ? Integer(match[1]) : 1
    end
  end
end
