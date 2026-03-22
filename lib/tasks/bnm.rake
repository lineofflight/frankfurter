# frozen_string_literal: true

namespace :bnm do
  desc "Backfill BNM rates"
  task :backfill do
    require "providers/bnm"
    Providers::BNM.backfill(range: 30)
  end
end
