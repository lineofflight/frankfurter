# frozen_string_literal: true

require "json"
require "money/currency"

# Patches the Money gem's currency table:
# - Registers historical ISO 4217 codes (pre-euro, pre-redenomination) the gem doesn't include.
# - Corrects names where the gem mangled an acronym (e.g. "Cfa" → "CFA").
# Existing entries are merged (preserving symbol, iso_numeric, etc.); missing ones are registered fresh.
JSON.parse(File.read(File.expand_path("../db/seeds/currency_patches.json", __dir__))).each do |entry|
  entry = entry.transform_keys(&:to_sym)
  existing = Money::Currency.table[entry[:iso_code].downcase.to_sym]
  Money::Currency.register(existing ? existing.merge(entry) : entry)
end
