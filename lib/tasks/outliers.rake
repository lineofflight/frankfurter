# frozen_string_literal: true

desc "Scan all rates for outliers (dry-run by default, pass 'apply' to flag)"
task :outliers, [:mode] do |_t, args|
  require "rate"
  require "log"

  apply = args[:mode] == "apply"
  min_history = 30
  sigma_threshold = 5
  total_flagged = 0

  triples = Rate.unfiltered.select(:provider, :base, :quote).distinct.all

  triples.each do |triple|
    provider = triple[:provider]
    base = triple[:base]
    quote = triple[:quote]

    rates = Rate.unfiltered.where(provider:, base:, quote:, outlier: false).select_map(:rate)
    next if rates.size < min_history

    mean = rates.sum / rates.size.to_f
    variance = rates.sum { |r| (r - mean)**2 } / rates.size.to_f
    sd = Math.sqrt(variance)
    next if sd.zero?

    lower = mean - (sigma_threshold * sd)
    upper = mean + (sigma_threshold * sd)

    flagged = Rate.unfiltered
      .where(provider:, base:, quote:)
      .exclude(rate: lower..upper)

    flagged.select(:date, :rate).each do |row|
      Log.info("#{provider}: outlier #{base}/#{quote} #{row[:rate]} on #{row[:date]} (mean: #{mean.round(4)}, stddev: #{sd.round(4)})")
      total_flagged += 1
    end

    flagged.update(outlier: true) if apply
  end

  Log.info("Total: #{total_flagged} outliers #{apply ? "flagged" : "found (dry-run)"}")
end
