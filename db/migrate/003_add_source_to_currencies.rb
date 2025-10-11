# frozen_string_literal: true

Sequel.migration do
  up do
    alter_table :currencies do
      add_column :source_code, String, size: 10, default: "ECB"
      add_foreign_key [:source_code], :sources, key: :code
    end

    drop_index :currencies, [:date, :iso_code], concurrently: true
    add_index :currencies, [:date, :iso_code, :source_code], unique: true, concurrently: true
  end

  down do
    alter_table :currencies do
      drop_foreign_key [:source_code]
      drop_column :source_code
    end

    drop_index :currencies, [:date, :iso_code, :source_code], concurrently: true
    add_index :currencies, [:date, :iso_code], unique: true, concurrently: true
  end
end
