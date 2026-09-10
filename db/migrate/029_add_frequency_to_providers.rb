# frozen_string_literal: true

# The period each observation stands for, after SDMX FREQ: daily (default), monthly or quarterly. Distinct from
# publish_cadence, the release rhythm. Coarser than daily never blends and carries forward across its period (#646).
Sequel.migration do
  change do
    alter_table(:providers) do
      add_column :frequency, String, null: false, default: "daily"
    end
  end
end
