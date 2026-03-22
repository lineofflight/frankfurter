# frozen_string_literal: true

Sequel.migration do
  up do
    ["BOC", "CBA", "CBR", "NBP", "NBRB", "NBU", "BNM"].each do |provider|
      from(:rates).where(provider:).delete
    end
  end
end
