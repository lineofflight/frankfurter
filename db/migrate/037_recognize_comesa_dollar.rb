# frozen_string_literal: true

# CMD was retained from RBM as an unknown code. Registering the COMESA Dollar makes its existing observations eligible
# for the catalogue and blends. Daily blends need a full rebuild, so leave the table empty for the scheduler's existing
# ready? lifecycle. Grouped blends can retain unaffected history; drop only buckets whose RBM source includes CMD for
# the existing populate job to repair.
Sequel.migration do
  up do
    require "currency_summary"

    cmd = Sequel.|({ base: "CMD" }, { quote: "CMD" })
    next unless from(:rates).where(provider: "RBM").where(cmd).any?

    CurrencySummary.refresh(self, ["CMD", "MWK"])
    from(:blended_rates).delete
    {
      weekly_rates: :blended_weekly_rates,
      monthly_rates: :blended_monthly_rates,
    }.each do |source, target|
      buckets = from(source).where(provider: "RBM").where(cmd).select(:bucket_date)
      from(target).where(bucket_date: buckets).delete
    end
  end

  down do
    # Irreversible: CMD remains a registered accounting unit.
  end
end
