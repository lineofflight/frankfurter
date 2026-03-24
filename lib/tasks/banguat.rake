# frozen_string_literal: true

namespace :banguat do
  desc "Backfill Banguat rates"
  task :backfill do
    require "providers/banguat"
    Providers::Banguat.backfill
  end
end
