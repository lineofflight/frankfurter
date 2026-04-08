# frozen_string_literal: true

Sequel.migration do
  up do
    create_table(:currencies) do
      String :iso_code, primary_key: true
      column :start_date, :date, null: false
      column :end_date, :date, null: false
    end

    create_table(:currency_coverages) do
      String :provider_key, null: false
      String :iso_code, null: false

      primary_key [:provider_key, :iso_code]
    end

    # Backfill currencies from rates
    run <<~SQL
      INSERT INTO currencies (iso_code, start_date, end_date)
      SELECT iso_code, MIN(start_date), MAX(end_date)
      FROM (
        SELECT quote AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
        FROM rates GROUP BY quote
        UNION ALL
        SELECT base AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
        FROM rates GROUP BY base
      )
      GROUP BY iso_code
      ORDER BY iso_code
    SQL

    # Backfill currency_coverages from rates
    run <<~SQL
      INSERT INTO currency_coverages (provider_key, iso_code)
      SELECT provider, iso_code FROM (
        SELECT DISTINCT provider, quote AS iso_code FROM rates
        UNION
        SELECT DISTINCT provider, base AS iso_code FROM rates
      )
      ORDER BY provider, iso_code
    SQL
  end

  down do
    drop_table(:currency_coverages)
    drop_table(:currencies)
  end
end
