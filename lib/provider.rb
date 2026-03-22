# frozen_string_literal: true

require "db"

class Provider < Sequel::Model(:providers)
  plugin :static_cache

  one_to_many :rates, key: :provider, primary_key: :key

  class << self
    def seed
      path = File.expand_path("../db/seeds/providers.json", __dir__)
      data = JSON.parse(File.read(path))
      data.each do |attrs|
        dataset.insert_conflict(target: :key).insert(attrs)
      end
      load_cache
    end
  end
end
