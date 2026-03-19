# frozen_string_literal: true

namespace :cba do
  desc "Backfill CBA rates"
  task :backfill do
    require "providers/cba"
    Providers::CBA.backfill
  end
end
