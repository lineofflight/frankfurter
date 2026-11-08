# frozen_string_literal: true

# Rates and everything derived per series now belong to a dataset. Every provider's one dataset is keyed like the
# provider, so the stored keys stay as they are. SQLite renames a column in place, rewriting the schema only: the
# indexes and the generated rate column follow the new name, and no row is rewritten. Index names keep `provider`, since
# SQLite cannot rename an index and rebuilding the rates indexes would cost a long deploy for a cosmetic gain.
Sequel.migration do
  columns = {
    rates: :provider,
    weekly_rates: :provider,
    monthly_rates: :provider,
    rate_spikes: :provider,
    currency_coverages: :provider_key,
    currency_exclusions: :provider_key,
  }

  # Sequel caches column lists per process and a raw ALTER leaves them stale, so reload them after renaming.
  up do
    columns.each do |table, column|
      run("ALTER TABLE #{table} RENAME COLUMN #{column} TO dataset_key")
      schema(table, reload: true)
    end
  end

  down do
    columns.each do |table, column|
      run("ALTER TABLE #{table} RENAME COLUMN dataset_key TO #{column}")
      schema(table, reload: true)
    end
  end
end
