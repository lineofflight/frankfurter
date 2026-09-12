# frozen_string_literal: true

desc "Backfill rates from all providers (incremental from last stored date)"
task :backfill, [:provider] do |_t, args|
  require "cache"
  require "provider"
  require "provider/adapters"

  if args[:provider]
    provider = Provider.detect { |p| p.key.casecmp(args[:provider]).zero? }
    abort "Unknown provider: #{args[:provider]}" unless provider
    provider.backfill
  else
    queue = Queue.new
    providers = Provider.to_a.shuffle
    providers.each { |provider| queue << provider }

    worker_count = [providers.size, DB.pool.max_size].min

    Array.new(worker_count) do
      Thread.new do
        loop do
          provider = queue.pop(true)
          provider.backfill
        rescue ThreadError
          break
        end
      end
    end.each(&:join)
  end

  # The wave is over and the process is about to exit, so flush any purge the debounce deferred.
  Cache.purge_pending(ignore_window: true)
end

namespace :backfill do
  desc "Recover a provider's historical bid/ask components without changing stored effective rates"
  task :components, [:provider] do |_task, args|
    require "provider"
    require "provider/adapters"

    provider = Provider.detect { |p| p.key.casecmp(args[:provider].to_s).zero? }
    abort "Specify a provider: rake backfill:components[KEY]" unless provider

    totals = { updated: 0, missing: 0, revised: 0 }
    provider.adapter.fetch_each(after: provider.coverage_start) do |records|
      records.each { |record| record[:provider] = provider.key }
      counts = Rate.enrich_components(records)
      counts.each { |key, value| totals[key] += value }
      Log.info("#{provider.key}: component enrichment #{counts}")
    end
    Log.info("#{provider.key}: component enrichment total #{totals}")
  end
end
