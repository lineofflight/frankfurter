# frozen_string_literal: true

desc "Import current rates from all providers"
task import: ["ecb:import", "boc:import", "tcmb:import", "nbu:import", "cba:import", "nbrb:import", "bob:import", "cbr:import"]

desc "Backfill all historical rates from all providers"
task backfill: ["ecb:backfill", "boc:backfill", "tcmb:backfill", "nbu:backfill", "cba:backfill", "nbrb:backfill", "bob:backfill", "cbr:backfill"]
