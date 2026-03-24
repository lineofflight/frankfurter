# frozen_string_literal: true

namespace :bccr do
  desc "Backfill BCCR rates"
  task :backfill do
    require "providers/bccr"
    Providers::BCCR.backfill
  end
end
