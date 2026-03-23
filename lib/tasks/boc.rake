# frozen_string_literal: true

namespace :boc do
  desc "Backfill BOC rates"
  task :backfill do
    require "providers/boc"
    Providers::BOC.backfill
  end
end
