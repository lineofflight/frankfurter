# frozen_string_literal: true

require "bank/importer"
require "currency"

module Bank
  class << self
    def fetch_all!
      data = normalize_data(importer.historical)
      Currency.dataset.insert_conflict.multi_insert(data)
    end

    def fetch_ninety_days!
      data = normalize_data(importer.ninety_days)
      Currency.dataset.insert_conflict.multi_insert(data)
    end

    def fetch_current!
      data = normalize_data(importer.current)
      Currency.dataset.insert_conflict.multi_insert(data) if data.any?
    end

    def replace_all!
      data = normalize_data(importer.historical)
      Currency.dataset.delete
      Currency.multi_insert(data)
    end

    def seed_with_saved_data!
      data = normalize_data(importer.saved_data)
      Currency.dataset.delete
      Currency.multi_insert(data)
    end

    private

    def importer
      @importer ||= Importer.new
    end

    def normalize_data(days)
      days.flat_map do |day|
        day[:rates].map do |iso_code, rate|
          {
            date: day[:date],
            iso_code: iso_code,
            rate: rate,
          }
        end
      end
    end
  end
end
