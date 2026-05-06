# frozen_string_literal: true

require "set"

require "peg"

# Peg-aware post-processing of blended rates. Substitutes peg rates for pegged quotes and synthesises rows for
# pegged currencies that providers do not cover. Operates on a single-base set; rebasing to the user's base is the
# caller's responsibility (in V2 this happens in RateQuery — `derive` on the pivot path or `scale_for_pegged_base`
# on the fast path).
#
# Pegs are treated as a source of rate data alongside providers. They contribute when the caller has not restricted
# the source set (no ?providers= filter). When a caller scopes to specific providers, peg behavior is bypassed at the
# call site by skipping this class entirely.
#
# Input contract: rows are already blended (one row per quote) and share a single :base.
#
# Two peg interactions are handled here:
#
#   1. A blended quote may be pegged. Its rate is replaced by the peg value, either directly (matched-base) or via
#      the peg's base as a bridge (cross-base).
#   2. A peg's quote may not be covered by any provider. A row is synthesized from the peg's anchor.
#
# Rows that came from a peg (rather than from a provider blend) carry no :providers key. This is the signal for
# callers (e.g. expand=providers) to omit provenance.
class PegAnchor
  class << self
    def apply(rows, base:)
      new(rows, base:).apply
    end
  end

  def initialize(rows, base:)
    @rows = rows
    @base = base
  end

  def apply
    return [] if @rows.empty?

    rows = @rows.map { |r| anchor_quote(r) }
    rows.concat(synthesized_pegs(rows))
    rows
  end

  private

  def reference_date
    @reference_date ||= @rows.map { |r| r[:date] }.max
  end

  def anchor_quote(row)
    peg = Peg.find(row[:quote])
    return row unless peg

    rate = if peg.base == @base
      peg.rate
    else
      bridge = @rows.find { |r| r[:quote] == peg.base }
      return row unless bridge

      bridge[:rate] * peg.rate
    end

    row.merge(rate: rate).tap { |h| h.delete(:providers) }
  end

  def synthesized_pegs(rows)
    emitted = rows.map { |r| r[:quote] }.to_set

    Peg.all.filter_map do |peg|
      next if peg.quote == @base
      next if emitted.include?(peg.quote)
      next if reference_date < peg.since

      rate = if peg.base == @base
        peg.rate
      else
        anchor = rows.find { |r| r[:quote] == peg.base }
        next unless anchor

        anchor[:rate] * peg.rate
      end

      { date: reference_date, base: @base, quote: peg.quote, rate: rate }
    end
  end
end
