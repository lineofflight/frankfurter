# frozen_string_literal: true

namespace :cba do
  desc "Import current CBA rates"
  task :import do
    require "providers/cba"
    Providers::CBA.new.current.import
  end

  desc "Import all historical CBA rates"
  task :backfill do
    require "providers/cba"
    Providers::CBA.new.historical.import
  end
end
