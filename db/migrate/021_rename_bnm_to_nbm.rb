# frozen_string_literal: true

Sequel.migration do
  up do
    from(:rates).where(provider: "BNM").update(provider: "NBM")
    from(:currency_coverages).where(provider_key: "BNM").update(provider_key: "NBM")

    # Clear stale NBM rollups from before the original rename, then re-key BNM.
    from(:weekly_rates).where(provider: "NBM").delete
    from(:weekly_rates).where(provider: "BNM").update(provider: "NBM")
    from(:monthly_rates).where(provider: "NBM").delete
    from(:monthly_rates).where(provider: "BNM").update(provider: "NBM")
  end

  down do
    from(:currency_coverages).where(provider_key: "NBM").update(provider_key: "BNM")
    from(:rates).where(provider: "NBM").update(provider: "BNM")
    from(:weekly_rates).where(provider: "NBM").update(provider: "BNM")
    from(:monthly_rates).where(provider: "NBM").update(provider: "BNM")
  end
end
