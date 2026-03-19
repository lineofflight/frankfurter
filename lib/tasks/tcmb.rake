# frozen_string_literal: true

namespace :tcmb do
  desc "Backfill TCMB rates"
  task :backfill do
    require "providers/tcmb"
    Providers::TCMB.backfill
  end
end
