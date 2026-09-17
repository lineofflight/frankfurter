# frozen_string_literal: true

Sequel.migration do
  up do
    drop_index(:weekly_rates, [:bucket_date, :provider], concurrently: true)
    [:weekly_rates, :monthly_rates].each do |table|
      add_index(table, [:bucket_date, :provider, :base, :quote], concurrently: true)
    end
  end

  down do
    [:weekly_rates, :monthly_rates].each do |table|
      drop_index(table, [:bucket_date, :provider, :base, :quote], concurrently: true)
    end
    add_index(:weekly_rates, [:bucket_date, :provider], concurrently: true)
  end
end
