# frozen_string_literal: true

Sequel.migration do
  change do
    alter_table(:providers) do
      add_column :publish_time, Integer
      add_column :publish_days, String
    end
  end
end
