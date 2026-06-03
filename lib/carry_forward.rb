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

    # Sliding-window equivalent of calling .apply once per anchor date, for the common case where
    # many ascending anchors share the same row set (range queries). Rows are grouped by key once;
    # a per-key cursor advances monotonically as anchors increase, so the whole set isn't rescanned
    # for every anchor. Yields [date, contributors] for each date — contributors is the identical
    # set .apply(rows, date:) would return (verified against it as an oracle in the specs).
    def each_snapshot(rows, dates:, lookback: LOOKBACK_DAYS)
      return to_enum(:each_snapshot, rows, dates:, lookback:) unless block_given?

      groups = rows.group_by { |r| [r[:provider], r[:base], r[:quote]] }
      groups.each_value { |group| group.sort_by! { |r| r[:date] } }
      cursors = Hash.new(-1)

      dates.sort.each do |date|
        cutoff = date - lookback
        contributors = []
        groups.each do |key, group|
          i = cursors[key]
          i += 1 while i + 1 < group.size && group[i + 1][:date] <= date
          cursors[key] = i
          next if i.negative?

          latest = group[i]
          contributors << latest if latest[:date] >= cutoff
        end
        yield date, contributors
      end
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
