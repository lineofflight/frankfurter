# frozen_string_literal: true

class BaseConversion
  attr_reader :rates, :base

  def initialize(rates, base:)
    @rates = rates
    @base = base
  end

  def convert
    direct = {}
    unreached = {}

    rates.group_by { |r| r[:provider] }.each do |provider, group|
      converted = convert_group(group).map { |r| r.merge(provider:) }
      if converted.empty?
        unreached[provider] = group
      else
        direct[provider] = reconcile(converted)
      end
    end

    rows = direct.values.flatten
    return rows if unreached.empty? || rows.empty?

    fallback = rows.group_by { |r| r[:quote] }.to_h do |quote, group|
      [[base, quote], { rate: group.sum { |r| r[:rate] } / group.size }]
    end

    bridged = unreached.flat_map do |provider, group|
      reconcile(convert_group(group, fallback).map { |r| r.merge(provider:) })
    end
    rows + bridged
  end

  private

  # A provider can reach the same quote by more than one bridge during a pivot-currency transition (e.g. Banque du Liban
  # quoting against both LTL and EUR around Lithuania's 2015 euro adoption). Collapse such duplicates into one averaged
  # rate per date and quote rather than failing the query: a live 5xx is never the right answer to a provider quirk, and
  # cross-provider consensus already guards against genuine outliers downstream.
  def reconcile(rows)
    rows.group_by { |r| [r[:date], r[:quote]] }.map do |_, group|
      next group.first if group.size == 1

      mean = group.sum { |r| r[:rate] } / group.size
      group.first.merge(rate: mean)
    end
  end

  def convert_group(group, fallback = {})
    # Index rows by [base, quote] once so cross_convert resolves bridges in O(1) instead of rescanning the group per
    # row; keep the first occurrence to match Array#find semantics.
    index = fallback.dup
    group.each { |r| index[[r[:base], r[:quote]]] ||= r }

    group.filter_map do |rate|
      if rate[:base] == base
        rate.except(:provider)
      elsif rate[:quote] == base
        invert(rate)
      else
        cross_convert(rate, index)
      end
    end
  end

  def invert(rate)
    { date: rate[:date], base:, quote: rate[:base], rate: 1.0 / rate[:rate] }
  end

  def cross_convert(rate, index)
    bridge = index[[base, rate[:quote]]]
    if bridge
      return { date: rate[:date], base:, quote: rate[:base], rate: bridge[:rate] / rate[:rate] }
    end

    bridge = index[[base, rate[:base]]]
    if bridge
      return { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] * bridge[:rate] }
    end

    bridge = index[[rate[:base], base]]
    if bridge
      return { date: rate[:date], base:, quote: rate[:quote], rate: rate[:rate] / bridge[:rate] }
    end

    nil
  end
end
