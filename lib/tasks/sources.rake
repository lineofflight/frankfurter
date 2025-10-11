# frozen_string_literal: true

require_relative "../../boot"

namespace :db do
  desc "Seed sources"
  task :seed_sources do
    require "source"

    sources_data = [
      {
        code: "ECB",
        name: "European Central Bank",
        base_currency: "EUR",
      },
      # Future sources (require feed implementation):
      # { code: "BOC", name: "Bank of Canada", base_currency: "CAD" },
    ]

    sources_data.each do |data|
      Source.dataset.insert_conflict(target: :code, update: data).insert(data)
    end

    count = Source.count
    puts "Seeded #{count} source#{'s' unless count == 1}"
  end
end
