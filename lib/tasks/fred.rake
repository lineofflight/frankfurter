# frozen_string_literal: true

namespace :fred do
  desc "Backfill FRED rates"
  task :backfill do
    require "providers/fred"
    Providers::FRED.backfill
  end
end
