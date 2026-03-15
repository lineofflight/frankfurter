# frozen_string_literal: true

namespace :rates do
  desc "Load all"
  task :all do
    require "bank"
    Bank.fetch_all!
  end

  desc "Load current"
  task :current do
    require "bank"
    Bank.fetch_current!
  end

  desc "Seed with saved data"
  task :seed_with_saved_data do
    require "bank"
    Bank.seed_with_saved_data!
  end
end
