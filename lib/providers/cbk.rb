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
      "SWISS FRANC" => "CHF",
      "CAN DOLLAR" => "CAD",
      "AUSTRALIAN DOLLAR" => "AUD",
      "INDIAN RUPEE" => "INR",
      "SWEDISH KRONA" => "SEK",
      "NORWEGIAN KRONE" => "NOK",
      "CHINESE YUAN" => "CNY",
      "S. AFRICAN RAND" => "ZAR",
      "SA RAND" => "ZAR",
      "AE DIRHAM" => "AED",
      "UAE DIRHAM" => "AED",
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
      iso = resolve_currency(currency_name.strip)
      return unless iso

      rate_value = Float(rest[0])
      return if rate_value.zero?

      { provider: key, date:, base: iso, quote: "KES", rate: rate_value }
    rescue ArgumentError, TypeError
      nil
    end

    def resolve_currency(name)
      upname = name.upcase

      # Check for East African cross-rate patterns like "KEN SHILLING / USHS"
      if upname.include?("KEN SHILLING /")
        suffix = upname.split("/").last.strip
        return CURRENCY_MAP[suffix]
      end

      # Direct match
      CURRENCY_MAP[upname] || CURRENCY_MAP.find { |k, _| upname.include?(k) }&.last
    end
  end
end
