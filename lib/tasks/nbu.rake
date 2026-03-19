# frozen_string_literal: true

namespace :nbu do
  desc "Backfill NBU rates"
  task :backfill do
    require "providers/nbu"
    Providers::NBU.backfill
  end
end
