# frozen_string_literal: true

desc "Backfill rates from all providers (incremental from last stored date)"
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
  "bnm:backfill",
  "rba:backfill",
  "bcra:backfill",
  "cbk:backfill",
  "boj:backfill",
  "imf:backfill",
  "nbrm:backfill",
  "bceao:backfill",
]
