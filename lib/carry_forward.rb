# frozen_string_literal: true

# Carries forward each provider's most recent rate within a lookback window.
# Used for single-date queries (latest) and range query enrichment.
module CarryForward
  LATEST_LOOKBACK_DAYS = 14
  RANGE_LOOKBACK_DAYS = 5

  # Returns the most recent rate per (provider, base, quote) on or before
  # the target date, within the lookback window.
  def self.latest(rows, date:, lookback: LATEST_LOOKBACK_DAYS)
    cutoff = date - lookback
    best = {}

    rows.each do |row|
      d = row[:date]
      next unless d && d <= date && d >= cutoff

      key = [row[:provider], row[:base], row[:quote]]
      best[key] = row if !best[key] || d > best[key][:date]
    end

    best.values
  end

  # Enriches each date in the target range with carried-forward rates.
  # Returns { date => [rows] } where each date's rows include both same-day
  # rates and each provider's most recent rate within the lookback window.
  # Carried-forward rows keep their original dates so WeightedAverage can
  # discount them by staleness.
  def self.enrich(rows, range:, lookback: RANGE_LOOKBACK_DAYS)
    by_date = rows.group_by { |r| r[:date] }
    target_dates = by_date.keys.select { |d| range.cover?(d) }.sort

    index = {}
    rows.each do |row|
      key = [row[:provider], row[:base], row[:quote]]
      (index[key] ||= []) << row
    end
    index.each_value { |v| v.sort_by! { |r| r[:date] }.reverse! }

    target_dates.to_h do |date|
      cutoff = date - lookback
      group = index.filter_map do |_, dated_rows|
        dated_rows.find { |r| r[:date] <= date && r[:date] >= cutoff }
      end
      [date, group]
    end
  end
end
