# frozen_string_literal: true

Sequel.migration do
  up do
    from(:rates).where(provider: "TCMB").delete
  end
end
