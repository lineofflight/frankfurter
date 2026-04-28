# frozen_string_literal: true

require "set"

require "peg"

# Peg-aware post-processing of blended rates. Applies peg substitutions, synthesizes rows for pegged currencies that
# providers do not cover, and rebases output to the user's base when the request base is itself pegged.
#
# Pegs are treated as a source of rate data alongside providers. They contribute when the caller has not restricted
# the source set (no ?providers= filter). When a caller scopes to specific providers, peg behavior is bypassed at the
# call site by skipping this class entirely.
#
# Input contract: rows are already blended (one row per quote in some `effective_base`). The caller has chosen the
# blend base based on whether the user's request base is pegged: if so, the blend ran in the peg's base; if not, in
# the user's base directly. PegAnchor receives the resulting rows plus the optional `base_peg` for the user's base.
#
# Three peg interactions are handled here:
#
#   1. The request's base may itself be pegged (e.g. base=AED). The blend ran in the peg's base (USD); rates are
#      then scaled to the user's base by 1/peg.rate, and a row is added for the peg's base.
#   2. A blended quote may be pegged. Its rate is replaced by the peg value, either directly (matched-base) or via
#      the peg's base as a bridge (cross-base).
#   3. A peg's quote may not be covered by any provider. A row is synthesized from the peg's anchor.
#
# Rows that came from a peg (rather than from a provider blend) carry no :providers key. This is the signal for
# callers (e.g. expand=providers) to omit provenance.
class PegAnchor
  class << self
    def apply(rows, base:, base_peg:)
      new(rows, base:, base_peg:).apply
    end
  end

  def initialize(rows, base:, base_peg:)
    @rows = rows
    @base = base
    @base_peg = base_peg
  end

  def apply
    return [] if @rows.empty?

    rows = @rows.map { |r| anchor_quote(r) }
    rows.concat(synthesized_pegs(rows))
    rows = scale_to_user_base(rows)
    rows.concat(base_peg_row(rows))
    rows
  end

  private

  def effective_base
    @base_peg&.base || @base
  end

  def reference_date
    @reference_date ||= @rows.map { |r| r[:date] }.max
  end

  def anchor_quote(row)
    peg = Peg.find(row[:quote])
    return row unless peg

    rate = if peg.base == effective_base
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

      rate = if peg.base == effective_base
        peg.rate
      else
        anchor = rows.find { |r| r[:quote] == peg.base }
        next unless anchor

        anchor[:rate] * peg.rate
      end

      { date: reference_date, base: effective_base, quote: peg.quote, rate: rate }
    end
  end

  def scale_to_user_base(rows)
    return rows unless @base_peg

    rows.map { |r| r.merge(rate: r[:rate] / @base_peg.rate, base: @base) }
  end

  def base_peg_row(rows)
    return [] unless @base_peg
    return [] if rows.any? { |r| r[:quote] == @base_peg.base }

    [{ date: reference_date, base: @base, quote: @base_peg.base, rate: 1.0 / @base_peg.rate }]
  end
end
