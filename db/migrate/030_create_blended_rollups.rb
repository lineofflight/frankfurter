# frozen_string_literal: true

Sequel.migration do
  up do
    [:blended_weekly_rates, :blended_monthly_rates].each do |table|
      create_table(table) do
        Date :bucket_date, null: false
        String :quote, null: false
        Float :rate, null: false
        primary_key [:quote, :bucket_date]
        index :bucket_date
      end
    end
  end

  down do
    drop_table(:blended_weekly_rates, :blended_monthly_rates)
  end
end
