# frozen_string_literal: true

# Process-wide cap on concurrent heavy range computes (#650). Live-path daily ranges recompute the blend per date, so a
# few full-history single-provider requests can hold every Puma thread for the better part of a minute while cheap
# shapes queue behind them; the request deadline bounds each of them, not their sum. Threads in one Puma worker share
# this counter and each worker gets its own, so the cap applies per process. Acquire never blocks: past the cap the
# request fails fast with Busy (a 503 with Retry-After) instead of waiting for a thread.
class HeavySlots
  class Busy < StandardError; end

  DEFAULT_MAX = Integer(ENV.fetch("MAX_HEAVY_COMPUTES", 2))
  RETRY_AFTER_SECONDS = 30

  attr_reader :max

  def initialize(max = DEFAULT_MAX)
    @max = max
    @held = 0
    @mutex = Mutex.new
  end

  def held
    @mutex.synchronize { @held }
  end

  def try_acquire
    @mutex.synchronize do
      return false if @held >= @max

      @held += 1
      true
    end
  end

  def release
    @mutex.synchronize { @held -= 1 if @held.positive? }
  end
end
