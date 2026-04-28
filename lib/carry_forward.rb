# frozen_string_literal: true

# Produces a snapshot of rates as of a target date by carrying forward each provider's most recent
# rate within a lookback window. Used for single-date and latest queries; range queries do not
# carry forward.
class CarryForward
  LOOKBACK_DAYS = 14

  class << self
    def apply(rows, date:, lookback: LOOKBACK_DAYS)
      new(rows, date:, lookback:).apply
    end
  end

  attr_reader :rows, :date, :lookback

  def initialize(rows, date:, lookback:)
    @rows = rows
    @date = date
    @lookback = lookback
  end

  def apply
    best = {}
    eligible_rows.each do |row|
      key = key_for(row)
      best[key] = row if !best[key] || row[:date] > best[key][:date]
    end

    best.values
  end

  private

  def cutoff
    @cutoff ||= date - lookback
  end

  def eligible_rows
    rows.select { |r| r[:date].between?(cutoff, date) }
  end

  def key_for(row)
    [row[:provider], row[:base], row[:quote]]
  end
end
