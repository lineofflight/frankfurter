# frozen_string_literal: true

require "quote/base"

module Quote
  class Interval < Base
    def formatted
      result_hash = {
        amount:,
        base:,
        start_date: result.keys.first,
        end_date: result.keys.last,
        rates: result,
      }
      result_hash[:source] = source if source
      result_hash.compact
    end

    def cache_key
      return if not_found?

      "#{result.keys.first}_#{result.keys.last}-#{source}"
    end

    private

    def fetch_data
      require "currency"

      scope = Currency.between(date)
      scope = scope.by_source(source) if source
      scope = scope.only(*(symbols + [base])) if symbols
      scope.naked
    end
  end
end
