# frozen_string_literal: true

require "json"
require "money/currency"

# Registers historical ISO 4217 currencies that the Money gem doesn't include.
# These are pre-euro and pre-redenomination codes still present in provider data.
JSON.parse(File.read(File.expand_path("../db/seeds/historical_currencies.json", __dir__))).each do |entry|
  Money::Currency.register(entry.transform_keys(&:to_sym))
end
