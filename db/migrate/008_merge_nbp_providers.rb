# frozen_string_literal: true

Sequel.migration do
  up do
    # Delete NBP.B rows that overlap with NBP (pre-2004 when both tables
    # carried EUR, USD, etc.)
    run <<~SQL
      DELETE FROM rates
      WHERE provider = 'NBP.B'
        AND (date, base, quote) IN (
          SELECT date, base, quote FROM rates WHERE provider = 'NBP'
        )
    SQL

    self[:rates].where(provider: "NBP.B").update(provider: "NBP")
    self[:providers].where(key: "NBP.B").delete
  end

  down do
    raise Sequel::Error, "irreversible migration"
  end
end
