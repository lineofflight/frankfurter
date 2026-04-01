# frozen_string_literal: true

# Cross-provider consensus filter. Compares each provider's rebased rates against the median.
# Rates that deviate significantly are identified as outliers.
class Consensus
  MIN_PROVIDERS = 4
  MULTIPLIER = 10
  MIN_DEVIATION = 0.05

  attr_reader :outliers

  def initialize(rates)
    @rates = rates
    @outliers = Set.new
  end

  def find
    @found ||= filter
  end

  private

  def filter
    @rates.group_by { |r| r[:quote] }.each do |_quote, group|
      providers = group.map { |r| r[:provider] }.uniq
      next if providers.size < MIN_PROVIDERS

      values = group.map { |r| r[:rate] }
      med = median(values)
      mad = median(values.map { |v| (v - med).abs })
      threshold = [MULTIPLIER * mad, MIN_DEVIATION * med.abs].max

      group.each do |r|
        if (r[:rate] - med).abs > threshold
          @outliers << [r[:provider], r[:quote]]
        end
      end
    end

    @rates.reject { |r| @outliers.include?([r[:provider], r[:quote]]) }
  end

  def median(values)
    sorted = values.sort
    sorted[sorted.size / 2]
  end
end
