# frozen_string_literal: true

Sequel.migration do
  up do
    create_table(:currency_exclusions) do
      String :provider_key, null: false
      String :iso_code, null: false
      column :start_date, :date, null: false
      column :end_date, :date, null: false
      primary_key [:provider_key, :iso_code]
    end

    require "money/currency"
    codes = Money::Currency.table.keys.map { |code| code.to_s.upcase }
    unknown = self[:rates].exclude(base: codes).select(:provider, Sequel[:base].as(:iso_code), :date)
      .union(self[:rates].exclude(quote: codes).select(:provider, Sequel[:quote].as(:iso_code), :date), all: true)
    self[:currency_exclusions].insert(
      [:provider_key, :iso_code, :start_date, :end_date],
      unknown.select(:provider, :iso_code, Sequel.function(:min, :date), Sequel.function(:max, :date))
        .group(:provider, :iso_code),
    )
  end

  down do
    drop_table(:currency_exclusions)
  end
end
