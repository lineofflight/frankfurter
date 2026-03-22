# frozen_string_literal: true

namespace :bcra do
  desc "Backfill BCRA rates"
  task :backfill do
    require "providers/bcra"
    Providers::BCRA.backfill
  end
end
