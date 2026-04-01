# frozen_string_literal: true

Sequel.migration do
  up do
    alter_table(:rates) do
      drop_column :outlier
    end
  end

  down do
    alter_table(:rates) do
      add_column :outlier, :boolean, default: false
    end
  end
end
