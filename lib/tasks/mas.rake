# frozen_string_literal: true

namespace :mas do
  desc "Backfill MAS rates"
  task :backfill do
    require "providers/mas"
    Providers::MAS.backfill
  end
end
