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
    run "ALTER TABLE rates DROP COLUMN rate"
    run <<~SQL
      ALTER TABLE rates ADD COLUMN rate REAL GENERATED ALWAYS AS (
        COALESCE(mid,
          CASE
            WHEN provider = 'BOJA' AND bid = 0 THEN frankfurter_midpoint(ask, ask)
            ELSE frankfurter_midpoint(bid, ask)
          END
        )
      ) VIRTUAL
    SQL
  end
end
