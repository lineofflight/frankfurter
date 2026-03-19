# frozen_string_literal: true

namespace :nbp do
  desc "Backfill NBP rates"
  task :backfill do
    require "providers/nbp"
    Providers::NBP.backfill
  end
end
