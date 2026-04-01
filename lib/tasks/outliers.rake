# frozen_string_literal: true

desc "Scan all rates for outliers (dry-run by default, pass 'apply' to flag)"
task :outliers, [:mode] do |_t, args|
  require "consensus"

  apply = args[:mode] == "apply"
  total_flagged = 0

  dates = Rate.unfiltered.select(:date).distinct.order(:date).select_map(:date)

  dates.each do |date|
    result = apply ? Consensus.flag(date) : Consensus.find(date)
    total_flagged += result.outliers.size
  end

  Log.info("Total: #{total_flagged} outliers #{apply ? "flagged" : "found (dry-run)"}")
end
