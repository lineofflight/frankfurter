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
    Provider.map { |provider| Thread.new { provider.backfill } }.each(&:join)
  end
end
