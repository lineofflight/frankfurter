# frozen_string_literal: true

Sequel.migration do
  up do
    alter_table :providers do
      rename_column :url, :data_url
      add_column :terms_url, String
    end
  end

  down do
    alter_table :providers do
      drop_column :terms_url
      rename_column :data_url, :url
    end
  end
end
