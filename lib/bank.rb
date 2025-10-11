# frozen_string_literal: true

require "bank/feed"
require "currency"

module Bank
  class << self
    def fetch_all!
      data = normalize_data(Feed.historical.to_a)
      Currency.dataset.insert_conflict.multi_insert(data)
    end

    def fetch_ninety_days!
      data = normalize_data(Feed.ninety_days.to_a)
      Currency.dataset.insert_conflict.multi_insert(data)
    end

    def fetch_current!
      data = normalize_data(Feed.current.to_a)
      # Use multi_insert only if we have multiple records
      if data.is_a?(Array)
        Currency.dataset.insert_conflict.multi_insert(data)
      end
    end

    def replace_all!
      data = normalize_data(Feed.historical.to_a)
      Currency.dataset.delete
      Currency.multi_insert(data)
    end

    def seed_with_saved_data!
      data = normalize_data(Feed.saved_data.to_a)
      Currency.dataset.delete
      Currency.multi_insert(data)
    end

    private

    def normalize_data(days)
      days.flat_map do |day|
        day[:rates].map do |iso_code, rate|
          {
            date: day[:date],
            iso_code: iso_code,
            rate: rate,
            source_code: "ECB",
          }
        end
      end
    end
  end
end
