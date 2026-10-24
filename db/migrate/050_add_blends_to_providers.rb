# frozen_string_literal: true

# Lets a daily provider stay out of the blend and the currency catalogue, served only when requested by name (#770).
Sequel.migration do
  change do
    alter_table(:providers) do
      add_column :blends, TrueClass, null: false, default: true
    end
  end
end
