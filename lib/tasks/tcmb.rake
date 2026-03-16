# frozen_string_literal: true

namespace :tcmb do
  desc "Import current TCMB rates"
  task :import do
    require "providers/tcmb"
    Providers::TCMB.new.current.import
  end

  desc "Import all historical TCMB rates"
  task :backfill do
    require "providers/tcmb"
    Providers::TCMB.new.historical.import
  end
end
