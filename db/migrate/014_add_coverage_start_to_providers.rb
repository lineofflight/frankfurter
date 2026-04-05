# frozen_string_literal: true

Sequel.migration do
  change do
    add_column :providers, :coverage_start, :date
  end
end
