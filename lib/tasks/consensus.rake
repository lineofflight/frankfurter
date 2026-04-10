# frozen_string_literal: true

desc "Show outliers rates for last 365 days"
task "consensus:recent" do
  require "rate"
  require "blender"
  require "log"

  scan_dates(Date.today - 365, Date.today)
end

desc "Show outliers rates (all history or a given year)"
task :consensus, [:year] do |_t, args|
  require "rate"
  require "blender"
  require "log"

  if args[:year]
    year = Integer(args[:year])
    scan_dates(Date.new(year, 1, 1), Date.new(year, 12, 31))
  else
    scan_dates(Rate.min(:date), Rate.max(:date))
  end
end

def scan_dates(from, to)
  counts = Hash.new(0)
  total = 0

  dates = Rate.select(:date).distinct.where(date: from..to).order(:date).select_map(:date)

  dates.each do |date|
    rows = Rate.where(date:).all
    blender = Blender.new(rows, base: "EUR")
    blender.blend
    blender.outliers.each do |provider, quote|
      counts["#{provider} #{quote}"] += 1
      total += 1
    end
  end

  counts.sort_by(&:last).reverse_each do |combo, count|
    Log.info(format("%-25s %d", combo, count))
  end

  Log.info("Total: #{total} outliers across #{dates.size} dates")
end
