# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Autoridade Monetária de Macau (AMCM). Publishes daily interbank middle
    # exchange rates against the Macanese pataca (MOP) for 17 active foreign
    # currencies plus historical pre-euro entries.
    #
    # The endpoint accepts Begin/End in YYYYMMDD format. The API caps each
    # response at roughly four calendar months of data, so backfill chunks in
    # 90-day windows.
    #
    # Direction: provider publishes "1 foreign = X MOP" via the `usdMean`
    # field (despite the name), so foreign currency goes in base and MOP in
    # quote. The `unit` field gives the unit multiplier — JPY and KRW are
    # quoted per 100 units, so the per-unit rate is `usdMean / unit`.
    #
    # ECU (the European Currency Unit, published 1987-1998) is rewritten to
    # XEU, the corresponding ISO 4217 code. LIQ is a non-currency liquidity
    # indicator and is dropped downstream by Money::Currency.find.
    class AMCM < Adapter
      URL = "https://www.amcm.gov.mo/api/v1.0/cms/financial_info"
      CODE_ALIASES = { "ECU" => "XEU" }.freeze

      class << self
        def backfill_range = 90
      end

      def fetch(after:, upto: nil)
        end_date = upto || Date.today
        response = http.get(URL, params: {
          "QueryType" => "1",
          "Begin" => after.strftime("%Y%m%d"),
          "End" => end_date.strftime("%Y%m%d"),
        }).to_s

        parse(response).select { |r| r[:date].between?(after, end_date) }
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless data.is_a?(Hash)

        rows = data["data"]
        return [] unless rows.is_a?(Array)

        rows.filter_map do |row|
          code = row["currency"]
          next unless code.is_a?(String) && code.match?(/\A[A-Z]{3}\z/)

          date_str = row["date"]
          next unless date_str

          unit = row["unit"].to_f
          next if unit.zero?

          rate = row["usdMeanValue"].to_f
          next if rate <= 0

          {
            date: Date.parse(date_str),
            base: CODE_ALIASES.fetch(code, code),
            quote: "MOP",
            rate: rate / unit,
          }
        end
      end
    end
  end
end
