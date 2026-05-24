# frozen_string_literal: true

Sequel.migration do
  up do
    from(:rates).where(provider: "MONGOLBANK").update(provider: "BOM")
    from(:currency_coverages).where(provider_key: "MONGOLBANK").update(provider_key: "BOM")
    from(:weekly_rates).where(provider: "MONGOLBANK").update(provider: "BOM")
    from(:monthly_rates).where(provider: "MONGOLBANK").update(provider: "BOM")
  end

  down do
    from(:rates).where(provider: "BOM").update(provider: "MONGOLBANK")
    from(:currency_coverages).where(provider_key: "BOM").update(provider_key: "MONGOLBANK")
    from(:weekly_rates).where(provider: "BOM").update(provider: "MONGOLBANK")
    from(:monthly_rates).where(provider: "BOM").update(provider: "MONGOLBANK")
  end
end
