# frozen_string_literal: true

Sequel.migration do
  up do
    create_table :providers do
      String :key, primary_key: true
      String :name, null: false
      String :description
      String :url
    end
  end

  down do
    drop_table :providers
  end
end
