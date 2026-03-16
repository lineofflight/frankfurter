# frozen_string_literal: true

namespace :ecb do
  desc "Import current ECB rates"
  task :import do
    require "providers/ecb"
    Providers::ECB.new.current.import
  end

  desc "Import all historical ECB rates"
  task :backfill do
    require "providers/ecb"
    Providers::ECB.new.historical.import
  end

  desc "Seed database from saved ECB data"
  task :seed do
    require "providers/ecb"
    ecb = Providers::ECB.new
    xml = File.read(File.join(Dir.pwd, "db", "seeds", "ecb.xml"))
    Rate.dataset.delete
    Providers::ECB.new(dataset: ecb.parse(xml)).import
  end
end
