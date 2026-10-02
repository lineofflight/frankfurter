# frozen_string_literal: true

# Dumps the Money gem's currency table, before Frankfurter's patches, for go/internal/currency. Keys are the gem's table
# ids (upper-cased); an id may name an alias whose iso_code differs (GHC -> GHS). Regenerate after a money gem upgrade:
#
#   APP_ENV=test mise exec -- bundle exec ruby go/scripts/dump_money.rb > go/internal/currency/money.json

require "json"
require "money"

fields = [:iso_code, :name, :symbol, :iso_numeric, :subunit_to_unit]
table = Money::Currency.table.sort_by { |id, _| id.to_s }.to_h do |id, data|
  [id.to_s.upcase, fields.to_h { |f| [f, data[f]] }.compact]
end
puts JSON.pretty_generate(table)
