# frozen_string_literal: true

namespace :boi do
  desc "Backfill BOI rates"
  task :backfill do
    require "providers/boi"
    Providers::BOI.backfill
  end
end
