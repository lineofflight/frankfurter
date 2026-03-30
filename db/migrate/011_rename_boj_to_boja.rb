# frozen_string_literal: true

Sequel.migration do
  up do
    from(:rates).where(provider: "BOJ").update(provider: "BOJA")
    from(:providers).where(key: "BOJ").update(key: "BOJA")
  end

  down do
    from(:rates).where(provider: "BOJA").update(provider: "BOJ")
    from(:providers).where(key: "BOJA").update(key: "BOJ")
  end
end
