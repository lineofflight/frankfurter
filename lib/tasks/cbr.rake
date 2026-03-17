# frozen_string_literal: true

namespace :cbr do
  desc "Import current CBR rates"
  task :import do
    require "providers/cbr"
    Providers::CBR.new.current.import
  end

  desc "Import all historical CBR rates"
  task :backfill do
    require "providers/cbr"
    Providers::CBR.new.historical.import
  end
end
