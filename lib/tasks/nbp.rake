# frozen_string_literal: true

namespace :nbp do
  desc "Import current NBP rates"
  task :import do
    require "providers/nbp"
    Providers::NBP.new.current.import
  end

  desc "Import all historical NBP rates"
  task :backfill do
    require "providers/nbp"
    Providers::NBP.new.historical.import
  end
end
