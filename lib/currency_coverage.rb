# frozen_string_literal: true

require "db"

class CurrencyCoverage < Sequel::Model
  unrestrict_primary_key

  many_to_one :provider, key: :provider_key
  many_to_one :currency, key: :iso_code
end
