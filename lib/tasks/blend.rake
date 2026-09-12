# frozen_string_literal: true

desc "Rebuild daily, weekly and monthly materialized blends in place"
task "blend:rebuild" do
  require "blended_rate"
  require "blended_weekly_rate"
  require "blended_monthly_rate"
  require "cache"
  require "log"

  [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
    started = Time.now
    model.rebuild
    Log.info("blend:rebuild: #{model.table_name}: #{model.dataset.count} rows in #{(Time.now - started).round(1)}s")
  end
  # Rebuilds change served values (that is why they run), so cached responses must not outlive them.
  Cache.purge
end

desc "Compare table and live response bytes and require grouped materialized coverage"
task "blend:parity", [:samples] do |_t, args|
  require "blend_parity"

  samples = Integer(args[:samples] || 200)
  abort "blended_rates is empty; run rake blend:rebuild first" if BlendedRate.dataset.empty?

  report = BlendParity.run(samples:)
  puts report
  abort "parity failed or incomplete; see coverage above" unless report.passed?
end
