# frozen_string_literal: true

desc "Backfill all historical rates from all providers"
task backfill: [
  "ecb:backfill",
  "boc:backfill",
  "tcmb:backfill",
  "nbu:backfill",
  "cba:backfill",
  "nbrb:backfill",
  "bob:backfill",
  "cbr:backfill",
  "nbp:backfill",
  "fred:backfill",
]
