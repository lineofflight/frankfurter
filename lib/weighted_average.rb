# frozen_string_literal: true

# Recency-weighted averaging of rebased exchange rates. Rates within the grace period carry full weight.
# Beyond it, weight decays exponentially so stale rates contribute less without a hard cutoff.
class WeightedAverage
  DECAY_GRACE_DAYS = 3
  DECAY_RATE = 0.5

  def initialize(rates)
    @rates = rates
  end

  def calculate
    @rates.each { |r| r[:date] = Date.parse(r[:date]) unless r[:date].is_a?(Date) }

    reference_date = @rates.map { |r| r[:date] }.max
    return [] unless reference_date

    @rates.group_by { |r| r[:quote] }.sort.map do |_, group|
      weighted = group.map { |r| [r, recency_weight(reference_date - r[:date])] }
      total_weight = weighted.sum(&:last)
      rate = weighted.sum { |r, w| r[:rate] * w } / total_weight
      providers = group.map { |r| r[:provider] }.uniq.sort
      group.max_by { |r| r[:date] }.merge(rate:, providers:)
    end
  end

  private

  def recency_weight(days_old)
    Math.exp(-DECAY_RATE * [days_old - DECAY_GRACE_DAYS, 0].max)
  end
end
