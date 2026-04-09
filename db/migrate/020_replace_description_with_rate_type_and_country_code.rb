# frozen_string_literal: true

Sequel.migration do
  up do
    alter_table :providers do
      add_column :rate_type, String
      add_column :country_code, String, size: 2
      drop_column :description
    end
  end

  down do
    alter_table :providers do
      add_column :description, String
      drop_column :rate_type
      drop_column :country_code
    end
  end
end
