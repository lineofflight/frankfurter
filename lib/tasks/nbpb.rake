# frozen_string_literal: true

namespace :nbpb do
  desc "Backfill NBPB rates"
  task :backfill do
    require "providers/nbpb"
    Providers::NBPB.backfill
  end
end
