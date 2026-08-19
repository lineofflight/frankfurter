# frozen_string_literal: true

require "rate_precision"

# Rows stored before RatePrecision existed carry binary float noise from adapter arithmetic: midpoints, per-unit
# rescaling, cross rates. Single-provider responses echo stored digits verbatim, so the noise is visible on the API
# (issue #579). Re-backfilling the 46 affected providers from coverage_start would recompute the same values off decades
# of scraped HTML, so round in place instead. Roughly 690k of 10.8M rows move, in about four seconds.
#
# Rollups and blends are left alone: they are averages, so their trailing digits are synthetic either way, and both
# round on output.
Sequel.migration do
  up do
    from(:rates)
      .exclude(rate: RatePrecision.sql(:rate))
      .update(rate: RatePrecision.sql(:rate))
  end

  down do
    # Irreversible. The discarded digits were noise, so there is nothing to restore.
  end
end
