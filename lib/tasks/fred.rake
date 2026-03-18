# frozen_string_literal: true

namespace :fred do
  desc "Import current FRED rates"
  task :import do
    require "providers/fred"
    Providers::FRED.new.current.import
  end

  desc "Import all historical FRED rates"
  task :backfill do
    require "providers/fred"
    Providers::FRED.new.historical.import
  end
end
