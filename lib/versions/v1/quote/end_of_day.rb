# frozen_string_literal: true

require "versions/v1/quote/base"
require "digest"

module Versions
  class V1 < Roda
    module Quote
      class EndOfDay < Base
        def formatted
          {
            amount:,
            base:,
            date: result.keys.first,
            rates: result.values.first,
          }
        end

        def cache_key
          return if not_found?

          Digest::MD5.hexdigest(result.keys.first)
        end

        private

        def fetch_data
          require "currency"

          scope = Currency.where(source: "ECB").latest(date)
          scope = scope.only(*(symbols + [base])) if symbols

          scope.naked
        end
      end
    end
  end
end
