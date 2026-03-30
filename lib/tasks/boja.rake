# frozen_string_literal: true

namespace :boja do
  desc "Backfill BOJA rates"
  task :backfill do
    require "providers/boja"
    Providers::BOJA.backfill
  end
end
