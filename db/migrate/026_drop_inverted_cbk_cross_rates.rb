# frozen_string_literal: true

# CBK's table mixes two orientations: most rows are KES per foreign unit, but the four East African cross rates ("KES /
# USHS" and friends) are published foreign per KES. The adapter used to invert those four so every row read base:
# <foreign>, quote: "KES". 1/x almost never terminates, so that manufactured a repeating decimal from an exact published
# figure and stored it, where single-provider responses echo it verbatim (issue #585).
#
# The adapter now records them as published, which moves the pair from (<foreign>, KES) to (KES, <foreign>). That is
# part of the unique index, so the stale rows cannot be rewritten in place; and inverting them back would recover the
# published digits only by inference. Delete them instead and let the deploy-time re-backfill from coverage_start fetch
# the published values:
#
#   Provider["CBK"].backfill(after: Provider["CBK"].coverage_start)
#
# Rollup rows go too, so the stale orientation does not outlive the migration in weekly and monthly responses; the
# backfill rebuilds every bucket it touches. currency_coverages needs no change: coverage is per (provider, currency)
# regardless of which side of the pair the currency sits on.
Sequel.migration do
  up do
    quotes = ["UGX", "TZS", "RWF", "BIF"]

    [:rates, :weekly_rates, :monthly_rates].each do |table|
      from(table).where(provider: "CBK", base: quotes, quote: "KES").delete
    end
  end

  down do
    # Irreversible. The deleted rates were reciprocals of what CBK published; the adapter no longer computes them.
  end
end
