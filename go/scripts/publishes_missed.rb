# frozen_string_literal: true

# Records Ruby's Provider#publishes_missed for every seeded publish_schedule and cadence (plus the spec's) over a grid
# of end dates and reference offsets, for go/internal/provider's parity test. Run from the repository root under UTC,
# the zone production runs in (fugit evaluates cron in the process's local zone):
#
#   TZ=UTC APP_ENV=test mise exec -- bundle exec ruby go/scripts/publishes_missed.rb \
#     go/internal/provider/testdata/publishes_missed.json
require_relative "../../boot"
require "minitest/mock"
require "provider"
require "json"

START = Date.new(2024, 12, 20)
FINISH = Date.new(2026, 3, 1)
STEP = 4
OFFSETS = [1, 2, 3, 4, 6, 9, 15, 31, 47, 95, 140].freeze

dir = File.expand_path("../../db/seeds/providers", __dir__)
combos = Dir["#{dir}/*.json"].map do |f|
  data = JSON.parse(File.read(f))
  [data["publish_schedule"], data["publish_cadence"]]
end
combos += [
  ["*/30 21-23 * * 1", "weekly"],
  ["*/30 1-10 3-12 * *", "monthly"],
  ["0 12 1-10 1,4,7,10 *", "quarterly"],
  ["*/30 8-10 * * 1-5", "monthly"],
  ["0 18 * * 4", "weekly"],
  ["0 12 1-5 * *", "quarterly"],
]
combos = combos.reject { |schedule, _| schedule.nil? }.uniq.sort

out = combos.map do |schedule, cadence|
  provider = Provider.new do |p|
    p.key = "X"
    p.publish_schedule = schedule
    p.publish_cadence = cadence
  end
  counts = (START..FINISH).step(STEP).map do |end_date|
    OFFSETS.map do |offset|
      provider.stub(:end_date, end_date.to_s) { provider.publishes_missed(reference_date: end_date + offset) }
    end
  end
  { schedule:, cadence:, counts: }
end

File.write(ARGV.fetch(0), JSON.generate({ start: START.to_s, step: STEP, offsets: OFFSETS, combos: out }))
