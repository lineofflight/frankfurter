# frozen_string_literal: true

require "currency"
require "money/currency"

class CurrencyNames
  def cache_key
    Digest::MD5.hexdigest(currencies.first.date.to_s)
  end

  def formatted
    iso_codes.to_h do |iso_code|
      [iso_code, Money::Currency.find(iso_code).name]
    end
  end

  private

  def iso_codes
    currencies.map(&:iso_code).append("EUR").sort
  end

  def currencies
    @currencies ||= find_currencies
  end

  def find_currencies
    Currency.latest.all
  end
end
