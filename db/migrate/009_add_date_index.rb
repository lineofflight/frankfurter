# frozen_string_literal: true

Sequel.migration do
  up do
    add_index :rates, :date, concurrently: true
  end

  down do
    drop_index :rates, :date, concurrently: true
  end
end
