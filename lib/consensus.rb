# frozen_string_literal: true

# Cross-provider consensus filter. Compares each provider's rebased rates against the median.
# Rates that deviate significantly are identified as outliers.
class Consensus
  MIN_PROVIDERS = 4
  MULTIPLIER = 10
  MIN_DEVIATION = 0.05

  attr_reader :rates

  def initialize(rates)
    @rates = rates
  end

  def find
    rates - outliers
  end

  def outliers
    @outliers ||= find_outliers
  end

  private

  def find_outliers
    flagged = Set.new

    rates.group_by { |r| r[:quote] }.each_value do |group|
      providers = group.map { |r| r[:provider] }.uniq
      next if providers.size < MIN_PROVIDERS

      values = group.map { |r| r[:rate] }
      med = median(values)
      mad = median(values.map { |v| (v - med).abs })
      threshold = [MULTIPLIER * mad, MIN_DEVIATION * med.abs].max

      group.each do |r|
        if (r[:rate] - med).abs > threshold
          flagged << [r[:provider], r[:quote]]
        end
      end
    end

    rates.select { |r| flagged.include?([r[:provider], r[:quote]]) }
  end

  def median(values)
    sorted = values.sort
    sorted[sorted.size / 2]
  end
end
