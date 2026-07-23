# frozen_string_literal: true

require_relative "helper"
require "blend_parity"
require "blended_rate"

# Merge-blocking parity gate for the materialized blend (#570). The live pipeline is the oracle:
# both paths must serve byte-identical responses across generated shapes, with exactly two declared
# behavior changes, each asserted rather than ignored. If a third divergence class appears here,
# stop and rethink the design instead of widening the carve-outs.
describe "blend parity" do
  def query(params, force_live: false)
    q = Versions::V2::RateQuery.new(params)
    q.force_live = force_live
    q
  end

  it "serves byte-identical responses from the table across generated shapes" do
    BlendedRate.rebuild
    report = BlendParity.run(samples: 30, seed: 20260723)

    _(report.failures).must_be_empty
    _(report.shapes).must_be(:>, 30)
  end

  # The explain machinery itself needs exercising in CI: fixture shapes are usually byte-identical,
  # so without an engineered divergence a broken verifier would only ever surface against the full
  # prod copy. The aging scenario from the carve-out spec below drives a genuine snap-back
  # divergence through explain_divergence, and a tampered table row must be rejected.
  it "verifies engineered divergences and rejects tampered table values" do
    observed = Fixtures.business_day(40)
    stale = observed - 10
    range_start = observed + 12
    Rate.dataset.multi_insert([
      { provider: "T1", date: observed, base: "EUR", quote: "MXN", rate: 20.0 },
      { provider: "T1", date: observed, base: "EUR", quote: "USD", rate: 1.2 },
      { provider: "T2", date: stale, base: "EUR", quote: "MXN", rate: 40.0 },
      { provider: "T2", date: stale, base: "EUR", quote: "USD", rate: 1.2 },
    ])
    BlendedRate.rebuild

    harness = BlendParity.new(samples: 0, seed: 1)
    shape = { from: range_start.to_s, to: (range_start + 2).to_s, quotes: "MXN" }
    table = harness.send(:records, shape, force_live: false)
    live = harness.send(:records, shape, force_live: true)

    _(table).wont_equal(live)

    verified, reason = harness.send(:explain_divergence, shape, table, live)

    _(reason).must_be_nil
    _(verified).must_be(:>, 0)

    BlendedRate.where(quote: "MXN", date: observed).update(rate: 999.0)
    tampered = BlendParity.new(samples: 0, seed: 1)
    tampered_table = tampered.send(:records, shape, force_live: false)
    _, tampered_reason = tampered.send(:explain_divergence, shape, tampered_table, live)

    _(tampered_reason).wont_be_nil
  end

  # Carve-out 1: a snap-back row serves the canonical anchor-date value. Live computes the range
  # start's snapshot at the range-start anchor, so a contributor that ages out of the carry-forward
  # lookback between its observation date and the range start changes the live value; the table keeps
  # the value blended at the row's own date.
  it "serves the canonical anchor-date value for snap-back rows" do
    observed = Fixtures.business_day(40)
    stale = observed - 10
    range_start = observed + 12

    # Each fake provider carries its own EUR to USD bridge so the pivot rebase can use its rows.
    Rate.dataset.multi_insert([
      { provider: "T1", date: observed, base: "EUR", quote: "MXN", rate: 20.0 },
      { provider: "T1", date: observed, base: "EUR", quote: "USD", rate: 1.2 },
      { provider: "T2", date: stale, base: "EUR", quote: "MXN", rate: 40.0 },
      { provider: "T2", date: stale, base: "EUR", quote: "USD", rate: 1.2 },
    ])
    BlendedRate.rebuild

    # Asserted in the pivot frame: derive divides a whole batch by the base's rate at the
    # range-start anchor on both paths, so only the pivot-frame value isolates the carve-out.
    params = { from: range_start.to_s, to: (range_start + 2).to_s, quotes: "MXN", base: "USD" }
    table_row = query(params).to_a.find { |r| r[:quote] == "MXN" }
    live_row = query(params, force_live: true).to_a.find { |r| r[:quote] == "MXN" }
    canonical_row = query({ from: observed.to_s, to: observed.to_s, quotes: "MXN", base: "USD" }, force_live: true)
      .to_a.find { |r| r[:quote] == "MXN" }

    _(table_row[:date]).must_equal(observed.to_s)
    # By the range start, T2 has aged out of the lookback, so the live snap-back drops it.
    _(live_row[:rate]).wont_equal(canonical_row[:rate])
    _(table_row[:rate]).must_equal(canonical_row[:rate])
  end

  # Carve-out 1, existence variant: an observation the consensus filter masked at its own date's
  # anchor has no canonical value. Live can surface it retroactively once the masking cohort ages
  # out of the carry-forward lookback; the table never serves it.
  it "omits rows that were consensus-masked at their own anchor" do
    d0 = Fixtures.business_day(40)
    d1 = d0 + 9
    cohort = [
      ["C1", 19.0],
      ["C2", 19.1],
      ["C3", 18.9],
      ["C4", 19.05],
    ]
    rows = cohort.flat_map do |provider, rate|
      [
        { provider:, date: d0, base: "EUR", quote: "ZAR", rate: },
        { provider:, date: d0, base: "EUR", quote: "USD", rate: 1.08 },
      ]
    end
    # X's observation at d1 is an outlier while the cohort is in the lookback; once the cohort ages
    # out, X is alone, below the consensus minimum, and its masked observation would emerge.
    rows << { provider: "X", date: d1, base: "EUR", quote: "ZAR", rate: 99.0 }
    rows << { provider: "X", date: d1, base: "EUR", quote: "USD", rate: 1.08 }
    Rate.dataset.multi_insert(rows)
    BlendedRate.rebuild

    params = { from: (d0 - 2).to_s, to: (d1 + 14).to_s, quotes: "ZAR", base: "USD" }
    live_dates = query(params, force_live: true).to_a.select { |r| r[:quote] == "ZAR" }.map { |r| r[:date] }
    table_dates = query(params).to_a.select { |r| r[:quote] == "ZAR" }.map { |r| r[:date] }

    _(live_dates).must_include(d1.to_s)
    _(table_dates).must_include(d0.to_s)
    _(table_dates).wont_include(d1.to_s)

    # The emergence has no canonical value: anchored at its own date, the blend keeps ZAR masked.
    own_anchor = query({ from: d1.to_s, to: d1.to_s, quotes: "ZAR", base: "USD" }, force_live: true)
      .to_a.select { |r| r[:quote] == "ZAR" }

    _(own_anchor.map { |r| r[:date] }).wont_include(d1.to_s)
  end

  # Carve-out 2: range batches always blend via the pivot path. A batch whose contributor rows all
  # share the requested base used to blend directly in that base; consensus and weighting see
  # differently shaped numbers there, so the output legitimately differs from the pivot-frame value.
  it "blends range batches in the pivot frame even when every contributor shares the base" do
    era_start = Fixtures.business_day(100)
    era_end = Fixtures.business_day(80)
    Rate.dataset.where(date: (era_start - CarryForward::LOOKBACK_DAYS)..era_end)
      .exclude(provider: "ECB").delete
    Rate.dataset.multi_insert(
      [era_start, era_end].map do |date|
        [
          { provider: "T3", date:, base: "EUR", quote: "GBP", rate: 0.95 },
          { provider: "T3", date:, base: "EUR", quote: "USD", rate: 1.30 },
        ]
      end.flatten,
    )
    BlendedRate.rebuild

    params = { from: era_start.to_s, to: era_start.to_s, quotes: "GBP" }
    window = Rate.dataset.where(date: (era_start - CarryForward::LOOKBACK_DAYS)..era_start).naked.all
    contributors = CarryForward.apply(window, date: era_start)

    _(contributors.map { |r| r[:base] }.uniq).must_equal(["EUR"])

    fast = PegAnchor.apply(Blender.new(contributors, base: "EUR").blend, base: "EUR")
      .find { |r| r[:quote] == "GBP" }
    pivot = Versions::V2::RateQuery.allocate.send(
      :derive,
      PegAnchor.apply(Blender.new(contributors, base: "USD").blend, base: "USD"),
      target: "EUR",
    ).find { |r| r[:quote] == "GBP" }
    rounder = Object.new.extend(Roundable)

    # Precondition: the two frames genuinely disagree for this batch, beyond rounding.
    _(rounder.round(pivot[:rate])).wont_equal(rounder.round(fast[:rate]))

    [query(params).to_a, query(params, force_live: true).to_a].each do |records|
      gbp = records.find { |r| r[:quote] == "GBP" }

      _(gbp[:rate]).must_equal(rounder.round(pivot[:rate]))
    end
  end
end
