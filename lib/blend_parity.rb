# frozen_string_literal: true

require "oj"
require "set"

require "blended_rate"
require "currency"
require "money/currency"
require "versions/v2/rate_query"

# Replays table-eligible query shapes through both the materialized-table path and the live compute
# path and compares serialized bytes. One declared divergence is asserted rather than ignored (#570):
# snap-back rows serve the canonical anchor-date value, so when a quote's contributor set changed
# between its observation date and the range start, the table row may differ from what the live path
# computes at the range-start anchor. Canonicality is asserted in the pivot frame: derive divides a
# whole batch by the base's rate at the range-start anchor (a batch property, identical on both
# paths and pinned by the byte-equal fresh rows around it), so only the row's own pivot-frame value
# distinguishes canonical from aged. Any other byte difference is a failure. The second declared
# change, pivot-frame canonicalization of range batches, lives in emit_blended itself and so is
# exercised by the byte comparison on both paths.
class BlendParity
  Report = Struct.new(:shapes, :snapback_rows, :failures, keyword_init: true) do
    def passed?
      failures.empty?
    end

    def to_s
      lines = ["blend:parity: #{shapes} shapes compared, " \
        "#{snapback_rows} snap-back rows verified canonical, #{failures.size} failures"]
      failures.first(10).each { |f| lines << "  FAIL #{f[:shape].inspect}: #{f[:reason]}" }
      lines.join("\n")
    end
  end

  POPULAR_BASES = ["EUR", "USD", "GBP", "JPY", "CHF", "AED", "CAD", "TRY"].freeze

  class << self
    def run(samples:, seed: 42)
      new(samples:, seed:).run
    end
  end

  def initialize(samples:, seed:)
    @samples = samples
    @rng = Random.new(seed)
    @pivot_frame_cache = {}
  end

  def run
    failures = []
    snapback_rows = 0

    shapes = adversarial_shapes + Array.new(@samples) { random_shape }
    shapes.each do |shape|
      table = records(shape, force_live: false)
      live = records(shape, force_live: true)
      next if Oj.dump(table, mode: :compat) == Oj.dump(live, mode: :compat)

      verified, reason = explain_divergence(shape, table, live)
      if reason
        failures << { shape:, reason: }
      else
        snapback_rows += verified
      end
    end

    Report.new(shapes: shapes.size, snapback_rows:, failures:)
  end

  private

  def records(shape, force_live:)
    # No deadline: the forced-live replay of a full-history shape legitimately outlives the
    # request timeout; bounding it would abort the harness, not a client request.
    query = Versions::V2::RateQuery.new(shape, Float::INFINITY)
    query.force_live = force_live
    query.to_a
  end

  # A divergent shape passes only when every difference traces to the canonical-anchor-date rule:
  #
  #   - A record only the live path emits must be a consensus-masked emergence: the live pipeline
  #     anchored at the record's own date yields no row of that date for the quote, so the record has
  #     no canonical value and does not exist in table-served responses.
  #   - A record only the table emits must sit in the snap-back region (before the range start) and
  #     be canonical; this is the flip side of an emergence, where live's snap-back surfaced a
  #     masked observation date instead of the canonical one.
  #   - Records both paths emit must align in order and keys; a rate difference is allowed only when
  #     the table value verifies canonical in the pivot frame. Most such rows sit in the snap-back
  #     region, but they can also appear interior to the range at a base currency's coverage
  #     boundary (the euro's birth): live can first emit a row only once the derive base exists, at
  #     an anchor past the row's own date, with that anchor's decay weights.
  #
  # Returns [verified_row_count, nil] on success, [0, reason] on failure.
  def explain_divergence(shape, table, live)
    table_keys = table.map { |r| r.values_at(:date, :quote) }.to_set
    live_keys = live.map { |r| r.values_at(:date, :quote) }.to_set
    verified = 0

    live.reject { |r| table_keys.include?(r.values_at(:date, :quote)) }.each do |r|
      # The table emits at most one pre-range row per quote (the newest canonical row at the range
      # start); live can re-surface older superseded rows mid-range when a consensus flip reveals
      # them. Those, like masked emergences, have no place in the canonical sequence.
      superseded = r[:date] < shape[:from] &&
        table.any? { |t| t[:quote] == r[:quote] && t[:date] > r[:date] && t[:date] < shape[:from] }
      if superseded || canonical_rate(shape, r).nil?
        verified += 1
        next
      end

      return [0, "live-only record #{r[:quote]} on #{r[:date]} is neither a masked emergence nor superseded"]
    end

    table.reject { |r| live_keys.include?(r.values_at(:date, :quote)) }.each do |r|
      unless r[:date] < shape[:from] && pivot_pair_canonical?(shape, r)
        return [0, "table-only record #{r[:quote]} on #{r[:date]} is not a canonical snap-back row"]
      end

      verified += 1
    end

    common_table = table.select { |r| live_keys.include?(r.values_at(:date, :quote)) }
    common_live = live.select { |r| table_keys.include?(r.values_at(:date, :quote)) }
    common_table.zip(common_live) do |t, l|
      if t.values_at(:date, :base, :quote) != l.values_at(:date, :base, :quote)
        return [0, "record keys diverge at #{t.values_at(:date, :base, :quote)} vs #{l.values_at(:date, :base, :quote)}"]
      end
      next if t[:rate] == l[:rate]

      unless pivot_pair_canonical?(shape, t)
        reason = "rate mismatch for #{t[:quote]} on #{t[:date]} where the table value is not " \
          "canonical: table #{t[:rate]}, live #{l[:rate]}"
        return [0, reason]
      end

      verified += 1
    end

    [verified, nil]
  end

  def pivot_pair_canonical?(shape, record)
    # A derived base->PIVOT row carries the reciprocal of the base's rate, so its canonicality is
    # the base currency's; the pivot frame has no PIVOT-quoted row to probe directly.
    probe = if record[:quote] == BlendedRate::PIVOT
      { date: record[:date], quote: (shape[:base] || "EUR").to_s.upcase }
    else
      record
    end
    table_pivot = pivot_frame_rate(shape, probe, from: shape[:from], to: shape[:to], force_live: false)
    canonical = canonical_rate(shape, probe)
    !canonical.nil? && table_pivot == canonical
  end

  # The canonical value for a record is what a live range anchored at the record's own date computes,
  # in the pivot frame so no derive denominator muddies the comparison.
  def canonical_rate(shape, record)
    pivot_frame_rate(shape, record, from: record[:date], to: record[:date], force_live: true)
  end

  # The record's pivot-frame value over the given window, shaped like the original request but based
  # on the pivot so the blended value is emitted undivided. Memoized per resulting query, since a
  # full-history shape with several divergent records would otherwise replay per record.
  def pivot_frame_rate(shape, record, from:, to:, force_live:)
    params = shape.reject { |k, _| [:from, :to, :base].include?(k) }.merge(base: BlendedRate::PIVOT)
    params[:quotes] = [params[:quotes], record[:quote]].compact.join(",") if params[:quotes]
    params = params.merge(from:, to:).compact

    lookup = @pivot_frame_cache[[params, force_live]] ||= records(params, force_live:)
      .to_h { |r| [[r[:date], r[:quote]], r[:rate]] }
    lookup[[record[:date], record[:quote]]]
  end

  def coverage
    @coverage ||= begin
      first = Rate.dataset.min(:date)
      raise "no rates data" unless first

      Date.parse(first)..Date.parse(Rate.dataset.max(:date))
    end
  end

  def coverage_days
    (coverage.end - coverage.begin).to_i
  end

  def adversarial_shapes
    saturday = coverage.end - ((coverage.end.wday + 1) % 7)
    mid = coverage.begin + coverage_days / 2
    [
      { from: coverage.begin.to_s },
      { from: coverage.begin.to_s, quotes: "USD,GBP,JPY" },
      { from: (coverage.end - 60).to_s, base: "USD" },
      { from: (coverage.end - 60).to_s, base: "AED" },
      { from: (coverage.end - 90).to_s, quotes: "AED,XAU,USD" },
      { from: saturday.to_s, to: (saturday + 10).to_s },
      { from: mid.to_s, to: mid.to_s },
      { from: (coverage.end - 30).to_s },
    ]
  end

  def random_shape
    roll = @rng.rand
    span = if roll < 0.75
      @rng.rand(1..[60, coverage_days].min)
    elsif roll < 0.98
      @rng.rand([61, coverage_days].min..[730, coverage_days].min)
    else
      @rng.rand([731, coverage_days].min..[2200, coverage_days].min)
    end
    from = coverage.begin + @rng.rand(0..[coverage_days - span, 0].max)

    shape = { from: from.to_s, to: (from + span).to_s }
    shape[:base] = POPULAR_BASES.sample(random: @rng) if @rng.rand < 0.5
    shape[:quotes] = quote_pool.sample(@rng.rand(1..6), random: @rng).join(",") if @rng.rand < 0.5
    shape
  end

  def quote_pool
    @quote_pool ||= Currency.select_map(:iso_code).select { |c| Money::Currency.find(c) }
  end
end
