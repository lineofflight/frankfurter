# frozen_string_literal: true

Sequel.migration do
  up do
    drop_index :rates, [:provider, :date, :quote], concurrently: true
    add_index :rates, [:provider, :date, :base, :quote], unique: true, concurrently: true
  end

  down do
    drop_index :rates, [:provider, :date, :base, :quote], concurrently: true
    add_index :rates, [:provider, :date, :quote], unique: true, concurrently: true
  end
end
