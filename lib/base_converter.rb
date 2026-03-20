# frozen_string_literal: true

# Converts a single provider's rates for a given date to a new base currency by dividing each rate by the base
# currency's rate. Produces an inverse rate for the provider's native base when it differs from the requested base.
class BaseConverter
  attr_reader :rates, :base

  def initialize(rates, base:)
    @rates = rates
    @base = base
  end

  def convert
    return [] unless base_rate

    rates.filter_map do |rate|
      if rate[:base] == base
        { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] }
      elsif rate[:quote] == base
        { date: rate[:date], base:, quote: rate[:base], rate: 1.0 / base_rate }
      else
        { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] / base_rate }
      end
    end
  end

  private

  def base_rate
    @base_rate ||= begin
      direct = rates.find { |r| r[:quote] == base }
      inverse = rates.find { |r| r[:base] == base } unless direct

      if direct
        direct[:rate]
      elsif inverse
        1.0 / inverse[:rate]
      elsif rates.first[:base] == base
        1.0
      end
    end
  end
end
