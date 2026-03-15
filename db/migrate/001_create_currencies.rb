# frozen_string_literal: true

Sequel.migration do
  up do
    create_table :currencies do
      Date    :date,   null: false
      String  :base,   null: false
      String  :quote,  null: false
      Float   :rate,   null: false
      String  :source, null: false

      index [:source, :date, :quote], unique: true
    end
  end

  down do
    drop_table :currencies
  end
end
