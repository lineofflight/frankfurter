# frozen_string_literal: true

Sequel.migration do
  change do
    create_table(:blended_rates) do
      Date :date, null: false
      String :quote, null: false
      Float :rate, null: false
      primary_key [:quote, :date]
      index :date
    end
  end
end
