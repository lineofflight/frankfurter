# frozen_string_literal: true

require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Saudi Central Bank. Publishes SAR per unit of about 70 currencies for every calendar day from 2021-04-10. A day's
    # table is up by midnight Riyadh time and carries the previous day's market, so Sunday and Monday repeat Saturday.
    # Foreign is the base and SAR the quote (pivot-in-quote, as NBG and BBK). Values are rounded to five decimal places,
    # which leaves the weakest currencies (LBP, IDR, VND) with one or two significant figures; they are relayed as
    # published.
    #
    # The currency page renders from a JSON handler. Asked for a date (dd/mm/yyyy), it returns that day's whole table.
    # Codes are Reuters RICs ("EUR=") and the SDR is labelled SDR.
    #
    # Licence: the terms of use (see terms_url) allow use with reference to www.sama.gov.sa, limit copying to personal
    # use, and prohibit use for personal or commercial gain. Ships under the #611 rule with attribution and a courtesy
    # notice. Cite the Saudi Central Bank as the source.
    class SAMA < Adapter
      URL = "https://www.sama.gov.sa/en-US/_LAYOUTS/15/SAMA.Portal/PortalHandler.ashx"
      ALIASES = { "SDR" => "XDR" }.freeze
      # The pivot itself at 1, the euro's legacy currencies at their fixed conversion rates, and the pre-2018 ouguiya
      # frozen at one value until August 2021. All retired before the archive starts.
      EXCLUDED_CURRENCIES = ["CYP", "MRO", "MTL", "SAR", "SKK"].freeze

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        start_date = after ? after + 1 : end_date

        (start_date..end_date).flat_map do |date|
          sleep(0.5) unless date == start_date
          parse(http.get(URL, params: {
            op: "filterCurrenciesWithCount",
            isArabic: false,
            date: date.strftime("%d/%m/%Y"),
            page: 0,
            limit: 1000,
          },).to_s)
        end
      end

      def parse(json)
        JSON.parse(json).fetch("data").filter_map do |row|
          code = row.fetch("CurrencyCode").delete_suffix("=")
          next if EXCLUDED_CURRENCIES.include?(code)

          {
            date: Date.parse(row.fetch("CurrencyDate")),
            base: ALIASES.fetch(code, code),
            quote: "SAR",
            rate: row.fetch("CurrencyRate"),
          }
        end
      end
    end
  end
end
