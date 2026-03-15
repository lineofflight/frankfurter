# frozen_string_literal: true

require "quote/base"

module Quote
  class Interval < Base
    def formatted
      {
        amount:,
        base:,
        start_date: result.keys.first,
        end_date: result.keys.last,
        rates: result,
      }
    end

    def cache_key
      return if not_found?

      Digest::MD5.hexdigest(result.keys.last)
    end

    private

    def fetch_data
      require "currency"

      scope = Currency.where(source: "ECB").between(date)
      scope = scope.only(*(symbols + [base])) if symbols

      scope.naked
    end
  end
end
