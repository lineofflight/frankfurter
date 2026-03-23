# frozen_string_literal: true

namespace :nbrm do
  desc "Backfill NBRM rates"
  task :backfill do
    require "providers/nbrm"
    Providers::NBRM.backfill
  end
end
