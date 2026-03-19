# frozen_string_literal: true

namespace :cbr do
  desc "Backfill CBR rates"
  task :backfill do
    require "providers/cbr"
    Providers::CBR.backfill
  end
end
