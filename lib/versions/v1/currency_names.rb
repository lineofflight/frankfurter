# frozen_string_literal: true

require "roda"
require "rate"
require "money/currency"

module Versions
  class V1 < Roda
    class CurrencyNames
      def cache_key
        return if currencies.empty?

        Digest::MD5.hexdigest(currencies.first[:date].to_s)
      end

      def formatted
        return {} if currencies.empty?

        iso_codes.to_h do |iso_code|
          [iso_code, Money::Currency.find(iso_code).name]
        end
      end

      private

      def iso_codes
        currencies.map { |c| c[:quote] }.append("EUR").sort
      end

      def currencies
        @currencies ||= find_currencies
      end

      def find_currencies
        require "carry_forward"

        today = Date.today
        rows = Rate.where(provider: "ECB").where(date: (today - 14)..today).naked.all
        CarryForward.apply(rows, date: today)
      end
    end
  end
end
