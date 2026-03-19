# frozen_string_literal: true

namespace :boc do
  desc "Backfill BOC rates"
  task :backfill do
    require "providers/boc"
    Providers::BOC.backfill
  end

  desc "Seed database from saved BOC data"
  task :seed do
    require "providers/boc"
    json = File.read(File.join(Dir.pwd, "db", "seeds", "boc.json"))
    Providers::BOC.new(dataset: Providers::BOC.new.parse(json)).import
  end
end
