# frozen_string_literal: true

namespace :bob do
  desc "Backfill BOB rates"
  task :backfill do
    require "providers/bob"
    Providers::BOB.backfill
  end
end
