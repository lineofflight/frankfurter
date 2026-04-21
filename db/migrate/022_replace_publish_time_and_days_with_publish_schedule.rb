# frozen_string_literal: true

Sequel.migration do
  up do
    alter_table :providers do
      add_column :publish_schedule, String
      add_column :publish_cadence, String
      drop_column :publish_time
      drop_column :publish_days
    end
  end

  down do
    alter_table :providers do
      add_column :publish_time, Integer
      add_column :publish_days, String
      drop_column :publish_schedule
      drop_column :publish_cadence
    end
  end
end
