# frozen_string_literal: true

# TCMB, DAB and BCT each gain a second dataset for cash or banknote rates (#803), so their existing series takes a
# suffix: TCMB.FOREX, DAB.TRANSFER and BCT.INTERBANK. Each stays default, so its rows still report the bare provider key
# and nothing served changes. Only keys change: rollups, spikes, coverage and exclusions move with their rows, and blend
# tables hold no dataset key.
Sequel.migration do
  renames = { "TCMB" => "TCMB.FOREX", "DAB" => "DAB.TRANSFER", "BCT" => "BCT.INTERBANK" }
  tables = [:rates, :weekly_rates, :monthly_rates, :rate_spikes, :currency_coverages, :currency_exclusions]

  up do
    transaction do
      renames.each do |old_key, new_key|
        tables.each { |table| from(table).where(dataset_key: old_key).update(dataset_key: new_key) }
      end
    end
  end

  down do
    transaction do
      renames.each do |old_key, new_key|
        tables.each { |table| from(table).where(dataset_key: new_key).update(dataset_key: old_key) }
      end
    end
  end
end
