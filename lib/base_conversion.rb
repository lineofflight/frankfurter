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
      reconcile(converted)
    end
  end

  private

  # A provider can reach the same quote by more than one bridge during a pivot-currency transition
  # (e.g. Banque du Liban quoting against both LTL and EUR around Lithuania's 2015 euro adoption).
  # Collapse such duplicates into one averaged rate per quote rather than failing the query: a live
  # 5xx is never the right answer to a provider quirk, and cross-provider consensus already guards
  # against genuine outliers downstream.
  def reconcile(rows)
    rows.group_by { |r| [r[:date], r[:quote]] }.map do |_, group|
      next group.first if group.size == 1

      mean = group.sum { |r| r[:rate] } / group.size
      group.first.merge(rate: mean)
    end
  end

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
