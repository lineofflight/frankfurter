# frozen_string_literal: true

Sequel.migration do
  up do
    run "ALTER TABLE rates DROP COLUMN rate"
    run <<~SQL
      ALTER TABLE rates ADD COLUMN rate REAL GENERATED ALWAYS AS (
        COALESCE(mid,
          CASE
            WHEN provider = 'BOJA' AND bid = 0 THEN ask
            WHEN bid IS NOT NULL AND ask IS NOT NULL
              THEN CAST(printf('%.12g', (bid + ask) / 2.0) AS REAL)
          END
        )
      ) VIRTUAL
    SQL
  end

  down do
    # Retain the SQL resolver: the legacy callback is no longer registered. Migration 031 can still roll back by copying
    # the effective rates into mid before restoring the original rate column.
  end
end
