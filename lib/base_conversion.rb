# frozen_string_literal: true

class BaseConversion
  attr_reader :rates, :base

  def initialize(rates, base:)
    @rates = rates
    @base = base
  end

  def convert
    rates.group_by { |r| r[:provider] }.flat_map do |provider, group|
      converted = convert_group(group).map { |r| r.merge(provider:) }
      seen = {}
      converted.each do |r|
        key = [r[:date], r[:quote]]
        raise "ambiguous bridge: #{provider} produced #{key.inspect} twice" if seen[key]

        seen[key] = true
      end
      converted
    end
  end

  private

  def convert_group(group)
    group.filter_map do |rate|
      if rate[:base] == base
        rate.except(:provider)
      elsif rate[:quote] == base
        invert(rate)
      else
        cross_convert(rate, group)
      end
    end
  end

  def invert(rate)
    { date: rate[:date], base:, quote: rate[:base], rate: 1.0 / rate[:rate] }
  end

  def cross_convert(rate, group)
    bridge = group.find { |r| r[:base] == base && r[:quote] == rate[:quote] }
    if bridge
      return { date: rate[:date], base:, quote: rate[:base], rate: bridge[:rate] / rate[:rate] }
    end

    bridge = group.find { |r| r[:base] == base && r[:quote] == rate[:base] }
    if bridge
      return { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] * bridge[:rate] }
    end

    bridge = group.find { |r| r[:base] == rate[:base] && r[:quote] == base }
    if bridge
      return { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] / bridge[:rate] }
    end

    nil
  end
end
