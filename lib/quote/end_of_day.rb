# frozen_string_literal: true

require "quote/base"

module Quote
  class EndOfDay < Base
    def formatted
      result_hash = {
        amount:,
        base:,
        date: result.keys.first,
        rates: result.values.first,
      }
      result_hash[:source] = source if source
      result_hash.compact
    end

    def cache_key
      return if not_found?

      "#{result.keys.first}-#{source}"
    end

    private

    def fetch_data
      require "currency"

      scope = Currency.latest(date)
      scope = scope.by_source(source) if source
      scope = scope.only(*(symbols + [base])) if symbols
      scope.naked
    end
  end
end
