# frozen_string_literal: true

namespace :boc do
  desc "Import current BOC rates"
  task :import do
    require "providers/boc"
    Providers::BOC.new.current.import
  end

  desc "Import all historical BOC rates"
  task :backfill do
    require "providers/boc"
    Providers::BOC.new.historical.import
  end
end
