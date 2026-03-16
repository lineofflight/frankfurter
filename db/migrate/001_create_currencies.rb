# frozen_string_literal: true

Sequel.migration do
  up do
    create_table :rates do
      Date    :date,     null: false
      String  :base,     null: false
      String  :quote,    null: false
      Float   :rate,     null: false
      String  :provider, null: false

      index [:provider, :date, :quote], unique: true
    end
  end

  down do
    drop_table :rates
  end
end
