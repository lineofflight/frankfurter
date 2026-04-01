# frozen_string_literal: true

Sequel.migration do
  change do
    alter_table(:rates) do
      add_column :outlier, :boolean, default: false, null: false
    end
  end
end
