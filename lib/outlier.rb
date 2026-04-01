# frozen_string_literal: true

require "log"
require "rate"

class Outlier
  MIN_HISTORY = 30
  STATS_WINDOW = 365
  SIGMA_THRESHOLD = 10

  class << self
    def detect(provider:, base:, quote:, dates:, exclude_dates: [], apply: false)
      new(provider:, base:, quote:, dates:, exclude_dates:).detect(apply:)
    end
  end

  def initialize(provider:, base:, quote:, dates:, exclude_dates: [])
    @provider = provider
    @base = base
    @quote = quote
    @dates = dates
    @exclude_dates = exclude_dates
  end

  def detect(apply: false)
    return 0 unless stats

    flagged = Rate.unfiltered
      .where(provider: @provider, base: @base, quote: @quote, date: @dates)
      .exclude(rate: bounds)

    flagged.select(:date, :rate).each do |row|
      Log.info("#{@provider}: flagged outlier #{@base}/#{@quote} #{row[:rate]} on #{row[:date]} " \
        "(mean: #{mean.round(4)}, stddev: #{sd.round(4)})")
    end

    flagged.update(outlier: true) if apply

    flagged.count
  end

  def stats
    return @stats if defined?(@stats)

    scope = Rate.where(provider: @provider, base: @base, quote: @quote)
    scope = scope.exclude(date: @exclude_dates) unless @exclude_dates.empty?
    rates = scope.order(Sequel.desc(:date)).limit(STATS_WINDOW).select_map(:rate)

    return @stats = nil if rates.size < MIN_HISTORY

    @mean = rates.sum / rates.size.to_f
    variance = rates.sum { |r| (r - @mean)**2 } / rates.size.to_f
    @sd = Math.sqrt(variance)

    return @stats = nil if @sd.zero?

    @stats = { mean: @mean, sd: @sd }
  end

  private

  attr_reader :mean, :sd

  def bounds
    lower = mean - (SIGMA_THRESHOLD * sd)
    upper = mean + (SIGMA_THRESHOLD * sd)
    lower..upper
  end
end
