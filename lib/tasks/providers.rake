# frozen_string_literal: true

desc "Backfill rates (incremental by default; FULL=1 starts from coverage_start)"
task :backfill, [:provider] do |_t, args|
  require "cache"
  require "provider"
  require "provider/adapters"

  if args[:provider]
    provider = Provider.detect { |p| p.key.casecmp(args[:provider]).zero? }
    abort "Unknown provider: #{args[:provider]}" unless provider
    backfill_provider(provider)
  else
    queue = Queue.new
    providers = Provider.to_a.shuffle
    providers.each { |provider| queue << provider }

    worker_count = [providers.size, DB.pool.max_size].min

    Array.new(worker_count) do
      Thread.new do
        loop do
          provider = queue.pop(true)
          backfill_provider(provider)
        rescue ThreadError
          break
        end
      end
    end.each(&:join)
  end

  # The wave is over and the process is about to exit, so flush any purge the debounce deferred.
  Cache.purge_pending(ignore_window: true)
end

def backfill_provider(provider)
  if ENV["FULL"] == "1"
    provider.backfill(after: provider.coverage_start)
  else
    provider.backfill
  end
end
