# frozen_string_literal: true

namespace :ecb do
  desc "Backfill ECB rates"
  task :backfill do
    require "providers/ecb"
    Providers::ECB.backfill
  end
end
