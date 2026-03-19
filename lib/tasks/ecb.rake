# frozen_string_literal: true

namespace :ecb do
  desc "Backfill ECB rates"
  task :backfill do
    require "providers/ecb"
    Providers::ECB.backfill
  end

  desc "Seed database from saved ECB data"
  task :seed do
    require "providers/ecb"
    csv = File.read(File.join(Dir.pwd, "db", "seeds", "ecb.csv"))
    Rate.dataset.delete
    Providers::ECB.new(dataset: Providers::ECB.new.parse(csv)).import
  end
end
