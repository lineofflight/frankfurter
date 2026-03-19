# frozen_string_literal: true

namespace :nbrb do
  desc "Backfill NBRB rates"
  task :backfill do
    require "providers/nbrb"
    Providers::NBRB.backfill
  end
end
