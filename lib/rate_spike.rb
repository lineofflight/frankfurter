# frozen_string_literal: true

require "carry_forward"
require "date"
require "db"

# A provider's one-day typo: an observation at least FACTOR times off both its previous and next observation of the same
# pair, in the same direction, while those two agree within FACTOR of each other. A move that persists (a devaluation, a
# redenomination) never qualifies, since the next observation stays with it. `rates` keeps the published value and
# provider queries serve it; `blendable` leaves it out of blends, so the provider's previous observation carries forward
# in its place.
#
# Judging needs the next observation, so the latest one blends until its successor arrives and backfill rescreens it.
# Neighbours more than MAX_GAP_DAYS away are too far apart to tell a typo from a move, so a sparse series is never
# screened, and an insert can only change the screening of observations within MAX_GAP_DAYS of the inserted dates.
module RateSpike
  FACTOR = 3
  MAX_GAP_DAYS = CarryForward::LOOKBACK_DAYS

  class << self
    def dataset
      DB[:rate_spikes]
    end

    # Spikes among the given rates rows, as provider, date, base, quote. Neighbours come from the same rows, so the
    # scope must reach MAX_GAP_DAYS past every observation to be judged.
    def detect(rates)
      window = { partition: [:provider, :base, :quote], order: :date }
      neighbours = rates.select(
        :provider, :date, :base, :quote, :rate,
        Sequel.function(:lag, :rate).over(**window).as(:prev_rate),
        Sequel.function(:lead, :rate).over(**window).as(:next_rate),
        Sequel.function(:lag, :date).over(**window).as(:prev_date),
        Sequel.function(:lead, :date).over(**window).as(:next_date),
      ).from_self

      # SQLite's two-argument max and min are scalar and return NULL when either neighbour is missing.
      high = Sequel.function(:max, :prev_rate, :next_rate)
      low = Sequel.function(:min, :prev_rate, :next_rate)
      days = ->(from, to) { Sequel.function(:julianday, to) - Sequel.function(:julianday, from) }
      neighbours
        .where(days.call(:prev_date, :date) <= MAX_GAP_DAYS)
        .where(days.call(:date, :next_date) <= MAX_GAP_DAYS)
        .where(high < low * FACTOR)
        .where(Sequel.|(Sequel[:rate] >= high * FACTOR, Sequel[:rate] * FACTOR <= low))
        .select(:provider, :date, :base, :quote)
    end

    # Rescreens the provider's observations around newly inserted dates and returns the dates whose screening changed,
    # whose blends the caller must refresh. Typically the previous observation, now that its successor has arrived.
    def refresh(provider, dates)
      return [] if dates.empty?

      judged = (dates.min - MAX_GAP_DAYS)..(dates.max + MAX_GAP_DAYS)
      scope = DB[:rates].where(provider:, date: (judged.begin - MAX_GAP_DAYS)..(judged.end + MAX_GAP_DAYS))
      found = detect(scope).where(date: judged).all
      stored = dataset.where(provider:, date: judged)
      key = ->(row) { [row[:date].to_s, row[:base], row[:quote]] }
      changed = (found.to_set(&key) ^ stored.all.to_set(&key)).map { |date, _, _| Date.parse(date) }.uniq
      return [] if changed.empty?

      stored.delete
      dataset.multi_insert(found)
      changed
    end
  end
end
