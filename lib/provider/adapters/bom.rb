# frozen_string_literal: true

require "json"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Mongolia. Publishes daily statutory reference rates in MNT for 38 currencies
    # plus XAU and XAG. Mandate set by the Law on Currency Regulation, Article 5(2):
    # rates are the official reference for customs, tax, and accounting in Mongolia.
    #
    # The movement endpoint returns the entire archive (2001-01-02 onward) in one
    # ~5 MB JSON response regardless of the requested date range, so we fetch once
    # and slice client-side. No chunking (no backfill_range).
    #
    # Direction: provider publishes "1 foreign = X MNT", so foreign currency goes
    # in base and MNT in quote.
    #
    # Rates are strings with comma thousand-separators (e.g. "3,576.42").
    #
    # All quotes are per 1 unit of the foreign currency. High-denomination
    # currencies show as small fractions (e.g. IDR=0.20 MNT, VND=0.14 MNT,
    # KRW=2.36 MNT on 2026-05-22), which is mathematically consistent with the
    # other pairs on the same day — no per-100 or per-1000 normalization needed.
    # Sanity check vs USD=3576.42 MNT on 2026-05-22:
    #   USD/IDR ≈ 17,882 (real ≈ 16,000)
    #   USD/VND ≈ 25,546 (real ≈ 25,000)
    #   USD/KRW ≈ 1,515 (real ≈ 1,400)
    #
    # SDR is published under the non-ISO label "SDR" and rewritten to XDR
    # (the ISO 4217 code for Special Drawing Rights) on emit.
    #
    # XAU and XAG are stored per troy ounce in MNT, as published (no per-gram
    # conversion). E.g. XAU=16,172,267.24 MNT/oz on 2026-05-22.
    class BOM < Adapter
      ENDPOINT = URI("https://www.mongolbank.mn/en/currency-rate-movement/data")
      CODE_ALIASES = { "SDR" => "XDR" }.freeze

      def fetch(after: nil, upto: nil)
        body = JSON.generate(
          startDate: (after || Date.new(2001, 1, 1)).to_s,
          endDate: (upto || Date.today).to_s,
        )
        response = Net::HTTP.start(ENDPOINT.host, ENDPOINT.port, use_ssl: true) do |http|
          req = Net::HTTP::Post.new(ENDPOINT.path)
          req["Content-Type"] = "application/json"
          req["Accept"] = "application/json"
          req.body = body
          http.request(req)
        end

        records = parse(check!(response, "BOM").body)
        records = records.select { |r| r[:date] >= after } if after
        records = records.select { |r| r[:date] <= upto } if upto
        records
      end

      def parse(json)
        data = json.is_a?(String) ? JSON.parse(json) : json
        return [] unless data.is_a?(Hash)

        rows = data["data"]
        return [] unless rows.is_a?(Array)

        rows.flat_map { |row| parse_row(row) }
      end

      private

      def parse_row(row)
        date_str = row["RATE_DATE"]
        return [] unless date_str

        date = Date.parse(date_str)

        row.filter_map do |code, value|
          next if code == "RATE_DATE"
          next unless code.is_a?(String) && code.match?(/\A[A-Z]{3}\z/)
          next if value.nil? || value.to_s.strip.empty?

          rate = parse_rate(value)
          next if rate.nil? || rate.zero?

          base = CODE_ALIASES.fetch(code, code)
          { date:, base:, quote: "MNT", rate: }
        end
      end

      def parse_rate(value)
        Float(value.to_s.delete(","))
      rescue ArgumentError
        nil
      end
    end
  end
end
