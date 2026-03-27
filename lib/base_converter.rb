# frozen_string_literal: true

class BaseConverter
  attr_reader :rates, :base

  def initialize(rates, base:)
    @rates = rates
    @base = base
  end

  def convert
    rates.filter_map do |rate|
      if rate[:base] == base
        # pass through
        { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] }
      elsif rate[:quote] == base
        invert(rate)
      else
        cross_convert(rate)
      end
    end
  end

  private

  def invert(rate)
    { date: rate[:date], base:, quote: rate[:base], rate: 1.0 / rate[:rate] }
  end

  def cross_convert(rate)
    # Case A: both rates share the same quote (e.g. USD→CAD and EUR→CAD)
    bridge = rates.find { |r| r[:base] == base && r[:quote] == rate[:quote] }
    if bridge
      return { date: rate[:date], base:, quote: rate[:base], rate: bridge[:rate] / rate[:rate] }
    end

    # Case B: rate's base is quoted against the target (e.g. USD→JPY and EUR→USD)
    bridge = rates.find { |r| r[:base] == base && r[:quote] == rate[:base] }
    if bridge
      return { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] * bridge[:rate] }
    end

    # Case C: rate's base quotes the target (e.g. USD→JPY and USD→EUR)
    bridge = rates.find { |r| r[:base] == rate[:base] && r[:quote] == base }
    if bridge
      return { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] / bridge[:rate] }
    end

    nil
  end
end
