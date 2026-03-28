# frozen_string_literal: true

namespace :nb do
  desc "Backfill Norges Bank rates"
  task :backfill do
    require "providers/nb"
    Providers::NB.backfill
  end
end
