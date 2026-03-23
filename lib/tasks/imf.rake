# frozen_string_literal: true

namespace :imf do
  desc "Backfill IMF rates"
  task :backfill do
    require "providers/imf"
    Providers::IMF.backfill
  end
end
