# frozen_string_literal: true

require "blender"
require "carry_forward"
require "db"
require "peg_anchor"
require "rate"

# Materialized blend (#570): the deduped blended series in pivot base, one row per (quote, date) where the contributor
# set changed, everything up to and including peg anchoring. The table is the response for every plain shape, whether
# latest, single-date, or daily range (#573); per-request work that remains is derive to the requested base, quotes
# filtering, the identity row, and rounding.
#
# Each stored row is the canonical anchor-date value: the blend computed at the anchor equal to the row's own
# observation date. Later anchors can re-emit the same (quote, date) with different floats as other contributors age out
# of the carry-forward lookback; those echoes are never stored, which is what keeps a stored row a pure function of its
# contributor rows.
class BlendedRate < Sequel::Model(:blended_rates)
  PIVOT = "USD"
  CHUNK_MONTHS = 3

  unrestrict_primary_key

  class << self
    # Recomputes stored blends for every anchor date in the window. Mirrors the live pipeline verbatim: same fetch
    # shape, CarryForward.each_snapshot, Blender, PegAnchor, same ordering. A reimplementation would break float parity
    # with the live path, which the parity spec guards.
    def refresh(window)
      chunks(window).each { |chunk| refresh_chunk(chunk) }
    end

    # Rebuilds in place newest-first: existing chunks remain readable throughout the run so ready? stays true and
    # requests never fall back to live compute. Stale leading rows are pruned upfront under an immediate transaction so
    # an active-range shrink preserves readiness immediately. Chunks replace themselves transactionally, and a final
    # immediate sweep re-checks bounds to prune rows outside the active date range without racing concurrent backfills.
    def rebuild
      window = db.transaction(mode: :immediate) do
        first = Rate.blendable.min(:date)
        if first
          min = Date.parse(first)
          dataset.where { date < min }.delete
          min..Date.parse(Rate.blendable.max(:date))
        else
          dataset.delete
          nil
        end
      end
      return unless window

      chunks(window).reverse_each do |chunk|
        refresh_chunk(chunk)
      end

      db.transaction(mode: :immediate) do
        bounds = Rate.blendable.select(
          Sequel.function(:min, :date).as(:min),
          Sequel.function(:max, :date).as(:max),
        ).first

        if bounds && bounds[:min]
          active_window = Date.parse(bounds[:min])..Date.parse(bounds[:max])
          dataset.exclude(date: active_window).delete
        else
          dataset.delete
        end
      end
    end

    # The table serves reads only once it covers full history: an incremental refresh makes it non-empty long before
    # blend:rebuild has run, and serving a partial table would silently truncate historical ranges.
    def ready?
      first_rate = Rate.blendable.min(:date)
      !first_rate.nil? && dataset.min(:date) == first_rate
    end

    private

    def chunks(window)
      result = []
      cursor = window.begin
      while cursor <= window.end
        chunk_end = [(cursor >> CHUNK_MONTHS) - 1, window.end].min
        result << (cursor..chunk_end)
        cursor = chunk_end + 1
      end
      result
    end

    # BEGIN IMMEDIATE serializes the fetch, compute, and write against other writers, so a refresh never commits blends
    # computed from a snapshot another backfill has since changed. Inside a backfill's transaction it simply joins,
    # since that transaction already holds the write lock.
    def refresh_chunk(chunk)
      db.transaction(**(db.in_transaction? ? {} : { mode: :immediate })) do
        lookback_start = chunk.begin - CarryForward::LOOKBACK_DAYS
        rows = Rate.blendable.where(date: lookback_start..chunk.end).naked.all
        anchors = rows.map { |r| r[:date] }.uniq.select { |d| chunk.cover?(d) }.sort

        buffer = []
        CarryForward.each_snapshot(rows, dates: anchors) do |anchor, contributors|
          next if contributors.empty?

          blended = Blender.new(contributors, base: PIVOT).blend
          blended = PegAnchor.apply(blended, base: PIVOT)
          blended.each do |r|
            buffer << { date: r[:date], quote: r[:quote], rate: r[:rate] } if r[:date] == anchor
          end
        end

        dataset.where(date: chunk.begin..chunk.end).delete
        buffer.each_slice(1000) { |batch| dataset.multi_insert(batch) }
      end
    end
  end
end
