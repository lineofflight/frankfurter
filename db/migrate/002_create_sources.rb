# frozen_string_literal: true

Sequel.migration do
  up do
    create_table :sources do
      String  :code, size: 10, primary_key: true
      String  :name, size: 100, null: false
      String  :base_currency, size: 3
    end
  end

  down do
    drop_table :sources
  end
end
