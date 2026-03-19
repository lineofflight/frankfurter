# frozen_string_literal: true

require "rate"
require "money/currency"

module Versions
  class V1 < Roda
    class CurrencyNames
      def cache_key
        return if currencies.empty?

        Digest::MD5.hexdigest(currencies.first.date.to_s)
      end

      def formatted
        return {} if currencies.empty?

        iso_codes.to_h do |iso_code|
          [iso_code, Money::Currency.find(iso_code).name]
        end
      end

      private

      def iso_codes
        currencies.map(&:quote).append("EUR").sort
      end

      def currencies
        @currencies ||= find_currencies
      end

      def find_currencies
        Rate.where(provider: "ECB").latest.all
      end
    end
  end
end
