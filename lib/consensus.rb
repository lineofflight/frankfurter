# frozen_string_literal: true

require "log"
require "rate"
require "base_converter"

# Cross-provider consensus outlier detection. For a given date, converts all providers' rates to a common base,
# then compares each provider's value against the median. Rates that deviate significantly are flagged as outliers.
class Consensus
  MIN_PROVIDERS = 3
  MULTIPLIER = 10
  MIN_DEVIATION = 0.05
  COMMON_BASE = "EUR"

  attr_reader :outliers

  class << self
    def find(date) = new(date).find
    def flag(date) = new(date).find.flag
  end

  def initialize(date)
    @date = date
    @outliers = []
  end

  # Compares each provider's rebased rates against the cross-provider median. Populates #outliers.
  def find
    rates.group_by { |r| r[:quote] }.each do |_quote, group|
      providers = group.map { |r| r[:provider] }.uniq
      next if providers.size < MIN_PROVIDERS

      values = group.map { |r| r[:rate] }
      med = median(values)
      mad = median(values.map { |v| (v - med).abs })
      threshold = [MULTIPLIER * mad, MIN_DEVIATION * med.abs].max

      group.each do |r|
        @outliers << r if (r[:rate] - med).abs > threshold
      end
    end

    self
  end

  # Flags outlier providers and unflags any previously flagged providers that are now within consensus.
  def flag
    DB.transaction do
      Rate.unfiltered.where(date: @date, outlier: true).update(outlier: false)
      @outliers.map { |r| r[:provider] }.uniq.each do |provider|
        Rate.unfiltered.where(provider:, date: @date).update(outlier: true)
        Log.info("#{provider}: flagged outlier rates on #{@date}")
      end
    end

    self
  end

  private

  def rates
    @rates ||= Rate.unfiltered.where(date: @date).all.group_by { |r| r[:provider] }.flat_map do |provider, rows|
      BaseConverter.new(rows, base: COMMON_BASE).convert.map { |r| r.merge(provider:) }
    end
  end

  def median(values)
    sorted = values.sort
    sorted[sorted.size / 2]
  end
end
