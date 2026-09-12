# frozen_string_literal: true

require "blender"
require "db"
require "peg_anchor"

# Materializes the existing provider-rollup blend, not an average of daily blends. Every bucket retains the full input
# set and its unrounded USD values. Empty blends stay empty: reads conservatively fall back for those buckets rather
# than manufacturing an identity row or choosing an older materialized date.
module BlendedRollup
  PIVOT = "USD"
  BATCH_BUCKETS = 100

  class << self
    def included(model)
      model.extend(ClassMethods)
      model.unrestrict_primary_key
    end
  end

  module ClassMethods
    def refresh(buckets)
      # Callers include whole-history adapter batches. Bound row loading here, while joining ingestion's enclosing
      # transaction so every source insert and grouped replacement still rolls back together on failure.
      buckets.uniq.each_slice(BATCH_BUCKETS).sum { |batch| refresh_batch(batch) }
    end

    def rebuild
      dates = source.blendable.select(:bucket_date).distinct.order(:bucket_date).select_map(:bucket_date)
      refresh(dates.reverse)

      db.transaction(**(db.in_transaction? ? {} : { mode: :immediate })) do
        dataset.exclude(bucket_date: source.blendable.select(:bucket_date)).delete
      end
    end

    # Fill incomplete builds at startup without rewriting covered history. Legitimately empty USD blends can remain
    # missing forever; retry only those buckets, and report zero when nothing was materialized so no purge fires.
    def populate
      dates = source.blendable.exclude(bucket_date: dataset.select(:bucket_date))
        .select(:bucket_date).distinct.order(:bucket_date).select_map(:bucket_date)
      refresh(dates.reverse)
    end

    def ready?
      source.blendable.exclude(bucket_date: dataset.select(:bucket_date)).empty?
    end

    # Source dates decide snap-back, including buckets that produce no USD blend. Read coverage and values from one
    # snapshot so ingestion/rebuild cannot leave a request with a mixture of old dates and new rows. A partial build may
    # serve complete chunks immediately; every incomplete chunk stays live.
    def read(range)
      db.transaction do
        dates = source.blendable.between(range).select(:bucket_date).distinct.order(:bucket_date)
          .select_map(:bucket_date)
        records = dataset.where(bucket_date: dates).order(:bucket_date, :quote).naked.all
        next unless (dates - records.map { |row| row[:bucket_date] }.uniq).empty?

        records.each do |row|
          row[:date] = row.delete(:bucket_date)
          row[:base] = PIVOT
        end
      end
    end

    private

    def refresh_batch(buckets)
      db.transaction(savepoint: true, **(db.in_transaction? ? {} : { mode: :immediate })) do
        rows = source.blendable.where(bucket_date: buckets).order(:bucket_date, :quote).naked.all
        rows.each { |row| row[:date] = row.delete(:bucket_date) }
        records = rows.group_by { |row| row[:date] }.flat_map do |date, contributors|
          blended = PegAnchor.apply(Blender.new(contributors, base: PIVOT).blend, base: PIVOT)
          blended.map { |row| { bucket_date: date, quote: row[:quote], rate: row[:rate] } }
        end

        dataset.where(bucket_date: buckets).delete
        records.each_slice(1000) { |batch| dataset.multi_insert(batch) }
        records.size
      end
    end
  end
end
