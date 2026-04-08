# frozen_string_literal: true

desc "Backfill rates from all providers (incremental from last stored date)"
task :backfill, [:provider] do |_t, args|
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
end
