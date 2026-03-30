# frozen_string_literal: true

desc "Backfill rates from all providers (incremental from last stored date)"
task :backfill, [:provider] do |_t, args|
  require "providers"

  if args[:provider]
    provider = Providers.all.find { |p| p.key.casecmp(args[:provider]).zero? }
    abort "Unknown provider: #{args[:provider]}" unless provider
    provider.backfill
  else
    Providers.backfill
  end
end
