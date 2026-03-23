# frozen_string_literal: true

namespace :boj do
  desc "Backfill BOJ rates"
  task :backfill do
    require "providers/boj"
    Providers::BOJ.backfill
  end
end
