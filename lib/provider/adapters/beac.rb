# frozen_string_literal: true

require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banque des Etats de l'Afrique Centrale (Bank of Central African States).
    # Publishes daily reference rates for 13 currency pairs against the CFA Franc BEAC (XAF) on
    # the homepage taux_de_change widget. EUR/XAF is published at the fixed peg of 655.957.
    #
    # The homepage only exposes the current day's snapshot — no historical query parameter and
    # no bulk archive. Historical attestation would require iterating the Wayback Machine CDX
    # API, which is out of scope for this initial pass.
    #
    # Each pair is rendered as `XXX/XAF` with ACHAT (buy) and VENTE (sell) columns. We coerce
    # to mid via the simple average of the two, per the buy/sell pattern documented in #314.
    # Records are returned in the provider's native direction — foreign currency as base, XAF
    # as quote — matching the pivot-in-quote convention shared by NBG and BBK.
    #
    # The widget pulls flag SVGs from xe.com, so BEAC's published basket may be sourced from
    # an XE feed under the hood. We faithfully relay what BEAC publishes regardless.
    class BEAC < Adapter
      URL = "https://www.beac.int/"
      DOCUMENT_PATTERN = %r{
        <span\s+class="code_valeur"[^>]*>\s*
          ([A-Z]{3})/([A-Z]{3})\s*
        </span>.*?
        <div\s+id="middle"[^>]*>\s*([\d.,]+)\s*</div>.*?
        <div\s+id="right"[^>]*>\s*([\d.,]+)\s*</div>
      }mx
      DATE_PATTERN = %r{Date de valeur\s*:\s*(\d{2})/(\d{2})/(\d{4})}

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        return [] if after && after > end_date

        response = Net::HTTP.get(URI(URL))
        records = parse(response)
        return [] if records.empty?

        records.select { |r| (after.nil? || r[:date] > after) && r[:date] <= end_date }
      end

      def parse(html)
        date_match = html.match(DATE_PATTERN)
        return [] unless date_match

        date = Date.new(date_match[3].to_i, date_match[2].to_i, date_match[1].to_i)

        html.scan(DOCUMENT_PATTERN).filter_map do |base, quote, buy_str, sell_str|
          buy = parse_rate(buy_str)
          sell = parse_rate(sell_str)
          next if buy.nil? || sell.nil? || buy.zero? || sell.zero?

          rate = (buy + sell) / 2.0
          { date:, base: base, quote: quote, rate: rate }
        end
      end

      private

      def parse_rate(str)
        return if str.nil? || str.strip.empty?

        Float(str.tr(",", "."))
      rescue ArgumentError
        nil
      end
    end
  end
end
