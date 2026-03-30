# frozen_string_literal: true

desc "Backfill rates from all providers (incremental from last stored date)"
task :backfill do
  require "providers"
  Providers.enabled.each do |provider|
    Rake::Task["#{provider.key.downcase}:backfill"].invoke
  end
end
