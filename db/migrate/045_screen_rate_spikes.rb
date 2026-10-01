# frozen_string_literal: true

# Flags every stored one-day typo (see RateSpike) so blends leave it out, without touching what providers published.
# Backfill keeps the flags current from here on. The blend tables are left as they are: refreshing all history is a full
# rebuild, too slow for a migration that runs before the app starts, and clearing them would send every request to live
# compute until the scheduler rebuilds. Run `rake blend:rebuild` after deploy; it rebuilds in place.
Sequel.migration do
  up do
    require "rate_spike"

    create_table(:rate_spikes) do
      String :provider, null: false
      Date :date, null: false
      String :base, null: false
      String :quote, null: false
      primary_key [:provider, :base, :quote, :date]
    end

    from(:rate_spikes).insert([:provider, :date, :base, :quote], RateSpike.detect(from(:rates)))
  end

  down do
    drop_table(:rate_spikes)
  end
end
