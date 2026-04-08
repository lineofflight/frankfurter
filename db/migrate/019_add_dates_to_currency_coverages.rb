# frozen_string_literal: true

Sequel.migration do
  up do
    add_column :currency_coverages, :start_date, :date
    add_column :currency_coverages, :end_date, :date

    # Backfill from rates
    run <<~SQL
      UPDATE currency_coverages
      SET start_date = (
        SELECT MIN(date) FROM rates
        WHERE rates.provider = currency_coverages.provider_key
          AND (rates.quote = currency_coverages.iso_code OR rates.base = currency_coverages.iso_code)
      ),
      end_date = (
        SELECT MAX(date) FROM rates
        WHERE rates.provider = currency_coverages.provider_key
          AND (rates.quote = currency_coverages.iso_code OR rates.base = currency_coverages.iso_code)
      )
    SQL
  end

  down do
    drop_column :currency_coverages, :start_date
    drop_column :currency_coverages, :end_date
  end
end
