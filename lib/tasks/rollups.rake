# frozen_string_literal: true

desc "Rebuild weekly and monthly rollups (all or one provider)"
task "rollups:rebuild", [:provider] do |_t, args|
  require "bucket"
  require "blended_weekly_rate"
  require "blended_monthly_rate"
  require "cache"
  require "db"
  require "log"
  require "provider"

  if args[:provider]
    provider = Provider.detect { |p| p.key.casecmp(args[:provider]).zero? }
    abort "Unknown provider: #{args[:provider]}" unless provider
    rebuild_rollups(DB[:rates].where(provider: provider.key), provider.key)
  else
    rebuild_rollups(DB[:rates])
  end
  Cache.purge
end

def rebuild_rollups(source, provider = nil)
  scope = provider ? { provider: } : {}
  label = provider || "all"

  DB.transaction(savepoint: true) do
    DB[:weekly_rates].where(scope).delete
    DB[:monthly_rates].where(scope).delete

    DB[:weekly_rates].insert(
      [:bucket_date, :provider, :base, :quote, :rate],
      source.select(Bucket.week, :provider, :base, :quote, Sequel.function(:avg, :rate))
        .group(:provider, :base, :quote, Bucket.week),
    )

    DB[:monthly_rates].insert(
      [:bucket_date, :provider, :base, :quote, :rate],
      source.select(Bucket.month, :provider, :base, :quote, Sequel.function(:avg, :rate))
        .group(:provider, :base, :quote, Bucket.month),
    )

    BlendedWeeklyRate.rebuild
    BlendedMonthlyRate.rebuild

    Log.info("#{label}: rebuilt #{DB[:weekly_rates].where(scope).count} weekly, " \
             "#{DB[:monthly_rates].where(scope).count} monthly rollup rows")
  end
end
