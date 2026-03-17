# frozen_string_literal: true

namespace :nbrb do
  desc "Import current NBRB rates"
  task :import do
    require "providers/nbrb"
    Providers::NBRB.new.current.import
  end

  desc "Import all historical NBRB rates"
  task :backfill do
    require "providers/nbrb"
    Providers::NBRB.new.historical.import
  end
end
