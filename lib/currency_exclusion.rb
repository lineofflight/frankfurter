# frozen_string_literal: true

require "db"

class CurrencyExclusion < Sequel::Model
  unrestrict_primary_key

  many_to_one :dataset, key: :dataset_key
end
