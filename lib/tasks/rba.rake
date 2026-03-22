# frozen_string_literal: true

namespace :rba do
  desc "Backfill RBA rates"
  task :backfill do
    require "providers/rba"
    Providers::RBA.backfill
  end
end
