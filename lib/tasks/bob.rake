# frozen_string_literal: true

namespace :bob do
  desc "Import current BOB rates"
  task :import do
    require "providers/bob"
    Providers::BOB.new.current.import
  end

  desc "Import all historical BOB rates"
  task :backfill do
    require "providers/bob"
    Providers::BOB.new.historical.import
  end
end
