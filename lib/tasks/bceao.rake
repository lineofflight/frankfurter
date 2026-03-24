# frozen_string_literal: true

namespace :bceao do
  desc "Backfill BCEAO rates"
  task :backfill do
    require "providers/bceao"
    Providers::BCEAO.backfill
  end
end
