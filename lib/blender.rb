# frozen_string_literal: true

require "base_converter"

# Blends exchange rates from multiple providers into a single set by converting
# each provider's rates to a common base currency, then computing a
# recency-weighted average for currencies quoted by more than one provider.
#
# Providers publish on different schedules, so the "latest" rate from each may
# be from different dates. Rather than treating all dates as equal, we weight
# each rate by how fresh it is. Rates from the last few days (the grace period)
# carry full weight — this accommodates weekends and holidays where a 2-3 day
# gap is normal and meaningless. Beyond the grace period, weight decays
# exponentially so that stale rates contribute less and less without needing a
# hard cutoff.
class Blender
  attr_reader :rates, :base

  def initialize(rates, base:)
    @rates = rates
    @base = base
  end

  # Days within the grace window get full weight (1.0). Beyond it, weight
  # halves roughly every 1.4 days: day 4 ≈ 0.61, day 7 ≈ 0.14, day 10 ≈ 0.03.
  DECAY_GRACE_DAYS = 3
  DECAY_RATE = 0.5

  def blend
    rebased = rates.group_by { |r| r[:provider] }.flat_map do |_, provider_rows|
      BaseConverter.new(provider_rows, base:).convert
    end
    # Downsample queries return dates as strings (SQLite has no date type)
    rebased.each { |r| r[:date] = Date.parse(r[:date]) unless r[:date].is_a?(Date) }

    reference_date = rebased.map { |r| r[:date] }.max
    rebased.group_by { |r| r[:quote] }.sort.map do |_, group|
      weighted = group.map { |r| [r, recency_weight(reference_date - r[:date])] }
      total_weight = weighted.sum(&:last)
      rate = weighted.sum { |r, w| r[:rate] * w } / total_weight
      weighted.max_by { |_, w| w }.first.merge(rate:)
    end
  end

  private

  def recency_weight(days_old)
    Math.exp(-DECAY_RATE * [days_old - DECAY_GRACE_DAYS, 0].max)
  end
end
