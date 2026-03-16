# frozen_string_literal: true

namespace :boc do
  desc "Import current BOC rates"
  task :import do
    require "providers/boc"
    Providers::BOC.new.current.import
  end

  desc "Import all historical BOC rates"
  task :backfill do
    require "providers/boc"
    Providers::BOC.new.historical.import
  end

  desc "Seed database from saved BOC data"
  task :seed do
    require "providers/boc"
    json = File.read(File.join(Dir.pwd, "db", "seeds", "boc.json"))
    Providers::BOC.new(dataset: Providers::BOC.new.parse(json)).import
  end
end
