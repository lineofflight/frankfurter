# frozen_string_literal: true

desc "Rebuild weekly and monthly rollups (all or one provider)"
task "rollups:rebuild", [:provider] do |_t, args|
  require "db"
  require "provider"

  if args[:provider]
    provider = Provider.detect { |p| p.key.casecmp(args[:provider]).zero? }
    abort "Unknown provider: #{args[:provider]}" unless provider
    keys = provider.datasets.map(&:key)
    rebuild_rollups(DB[:rates].where(dataset_key: keys), provider.key, keys)
  else
    rebuild_rollups(DB[:rates])
  end
end

def rebuild_rollups(source, label = "all", dataset_keys = nil)
  require "bucket"
  require "blended_weekly_rate"
  require "blended_monthly_rate"
  require "cache"
  require "log"
  require "rate_rollup"

  scope = dataset_keys ? { dataset_key: dataset_keys } : {}
  models = [BlendedWeeklyRate, BlendedMonthlyRate]
  affected = {}

  DB.transaction(savepoint: true, **(DB.in_transaction? ? {} : { mode: :immediate })) do
    models.each do |model|
      affected[model] = model.source.blendable.where(scope).select(:bucket_date).distinct.select_map(:bucket_date)
    end
    DB[:weekly_rates].where(scope).delete
    DB[:monthly_rates].where(scope).delete

    DB[:weekly_rates].insert(RateRollup::COLUMNS, RateRollup.source(source, Bucket.week))
    DB[:monthly_rates].insert(RateRollup::COLUMNS, RateRollup.source(source, Bucket.month))

    models.each do |model|
      dates = model.source.blendable.where(scope).select(:bucket_date).distinct.select_map(:bucket_date)
      affected[model] |= dates
      # Include buckets removed by the rebuild, and invalidate before releasing the source write lock.
      model.dataset.where(bucket_date: affected[model]).delete
    end

    Log.info("#{label}: rebuilt #{DB[:weekly_rates].where(scope).count} weekly, " \
             "#{DB[:monthly_rates].where(scope).count} monthly rollup rows")
  end

  # Refill bounded batches after releasing the source transaction. Failures leave only affected buckets on the live
  # fallback, and cache invalidation still runs because the source changes have committed.
  begin
    affected.each { |model, dates| model.refresh(dates) unless dates.empty? }
  ensure
    Cache.purge
  end
end
