# frozen_string_literal: true

desc "Rebuild the materialized blend from scratch"
task "blend:rebuild" do
  require "blended_rate"
  require "cache"
  require "log"

  started = Time.now
  BlendedRate.rebuild
  Log.info("blend:rebuild: #{BlendedRate.dataset.count} rows in #{(Time.now - started).round(1)}s")
  # Rebuilds change served values (that is why they run), so cached responses must not outlive them.
  Cache.purge
end

desc "Replay query shapes through the table and live paths and compare bytes"
task "blend:parity", [:samples] do |_t, args|
  require "blend_parity"

  samples = Integer(args[:samples] || 200)
  abort "blended_rates is empty; run rake blend:rebuild first" if BlendedRate.dataset.empty?

  report = BlendParity.run(samples:)
  puts report
  abort "parity failed" unless report.passed?
end
