# frozen_string_literal: true

desc "Scan all rates for outliers (dry-run by default, pass 'apply' to flag)"
task :outliers, [:mode] do |_t, args|
  require "outlier"

  apply = args[:mode] == "apply"
  total_flagged = 0

  triples = Rate.unfiltered.select(:provider, :base, :quote).distinct.all

  triples.each do |triple|
    provider = triple[:provider]
    base = triple[:base]
    quote = triple[:quote]

    dates = Rate.unfiltered.where(provider:, base:, quote:, outlier: false)
      .order(Sequel.desc(:date)).limit(Outlier::STATS_WINDOW).select_map(:date)

    total_flagged += Outlier.detect(provider:, base:, quote:, dates:, apply:)
  end

  Log.info("Total: #{total_flagged} outliers #{apply ? "flagged" : "found (dry-run)"}")
end
