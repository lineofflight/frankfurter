# frozen_string_literal: true

namespace :cbk do
  desc "Backfill CBK rates"
  task :backfill do
    require "providers/cbk"
    Providers::CBK.backfill
  end
end
