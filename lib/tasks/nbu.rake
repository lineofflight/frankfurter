# frozen_string_literal: true

namespace :nbu do
  desc "Import current NBU rates"
  task :import do
    require "providers/nbu"
    Providers::NBU.new.current.import
  end

  desc "Import all historical NBU rates"
  task :backfill do
    require "providers/nbu"
    Providers::NBU.new.historical.import
  end
end
