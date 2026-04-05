# frozen_string_literal: true

Sequel.migration do
  up do
    from(:rates).where(provider: "BOT").update(provider: "BOTA")
    from(:providers).where(key: "BOT").update(key: "BOTA")
  end

  down do
    from(:rates).where(provider: "BOTA").update(provider: "BOT")
    from(:providers).where(key: "BOTA").update(key: "BOT")
  end
end
