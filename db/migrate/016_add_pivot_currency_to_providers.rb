# frozen_string_literal: true

Sequel.migration do
  change do
    add_column :providers, :pivot_currency, String
  end
end
