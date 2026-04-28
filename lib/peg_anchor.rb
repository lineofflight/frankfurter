# frozen_string_literal: true

require "blender"
require "peg"

# Peg-aware rate computation. Wraps Blender to apply peg substitutions and synthesize
# rows for pegged currencies that providers do not cover.
#
# Pegs are treated as a source of rate data alongside providers. They contribute when the
# caller has not restricted the source set (no ?providers= filter). When a caller scopes
# to specific providers, peg behavior is bypassed at the call site by using Blender directly
# rather than this class.
#
# Three peg interactions are handled here:
#
#   1. The request's base may itself be pegged (e.g. base=AED). Internally the blend runs
#      in the peg's base (USD), then rates are scaled to the user's base by 1/peg.rate.
#   2. A blended quote may be pegged. Its rate is replaced by the peg value, either directly
#      (matched-base) or via the peg's base as a bridge (cross-base).
#   3. A peg's quote may not be covered by any provider. A row is synthesized from the
#      peg's anchor.
#
# Rows that came from a peg (rather than from a provider blend) carry no :providers key.
# This is the signal for callers (e.g. expand=providers) to omit provenance.
class PegAnchor
  def initialize(rows, base:)
    @rows = rows
    @base = base
  end

  def blend
    return [] if blended.empty?

    rows = blended.map { |r| anchor_quote(r) }
    rows.concat(synthesized_pegs(rows))
    rows = scale_to_user_base(rows)
    rows.concat(base_peg_row(rows))
    rows
  end

  private

  def blended
    @blended ||= Blender.new(@rows, base: effective_base).blend
  end

  def effective_base
    base_peg&.base || @base
  end

  def base_peg
    return @base_peg if defined?(@base_peg)

    @base_peg = Peg.find(@base)
  end

  def reference_date
    @reference_date ||= blended.map { |r| r[:date] }.max
  end

  def anchor_quote(row)
    peg = Peg.find(row[:quote])
    return row unless peg

    rate = if peg.base == effective_base
      peg.rate
    else
      bridge = blended.find { |r| r[:quote] == peg.base }
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
    return rows unless base_peg

    rows.map { |r| r.merge(rate: r[:rate] / base_peg.rate, base: @base) }
  end

  def base_peg_row(rows)
    return [] unless base_peg
    return [] if rows.any? { |r| r[:quote] == base_peg.base }

    [{ date: reference_date, base: @base, quote: base_peg.base, rate: 1.0 / base_peg.rate }]
  end
end
