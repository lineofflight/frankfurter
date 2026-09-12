# frozen_string_literal: true

require_relative "helper"
require "blend_parity"
require "blended_rate"

# Merge-blocking parity gate for the materialized blend (#570). The live pipeline is the oracle: both paths must serve
# byte-identical responses across generated shapes, with exactly two declared behavior changes, each asserted rather
# than ignored. If a third divergence class appears here, stop and rethink the design instead of widening the
# carve-outs.
describe "blend parity" do
  it "serves byte-identical responses from the table across generated shapes" do
    [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
    report = BlendParity.run(samples: 30, seed: 20260723)

    _(report.failures).must_be_empty
    _(report.passed?).must_equal(true)
    ["week", "month"].each do |group|
      _(report.grouped_coverage[group][:materialized]).must_be(:>, 0)
      _(report.grouped_coverage[group][:fallback]).must_equal(0)
    end
    _(report.shapes).must_be(:>, 30)
  end
end

describe "grouped blend parity" do
  it "replays both grouped paths and rejects any changed stored value" do
    [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      model.where(quote: "GBP").update(rate: 99.0)
    end

    report = BlendParity.run(samples: 0)

    groups = report.failures.map { |failure| failure[:shape][:group] }.uniq

    _(groups).must_include("week")
    _(groups).must_include("month")
    _(report.snapback_rows).must_equal(0)
  end
end

describe "grouped parity coverage" do
  before do
    [BlendedRate, BlendedWeeklyRate, BlendedMonthlyRate].each(&:rebuild)
  end

  it "rejects live versus live comparisons when grouped tables are empty" do
    [BlendedWeeklyRate, BlendedMonthlyRate].each { |model| model.dataset.delete }

    report = BlendParity.run(samples: 0)

    _(report.passed?).must_equal(false)
    _(report.failures).must_be_empty
    ["week", "month"].each do |group|
      _(report.grouped_coverage[group][:materialized]).must_equal(0)
      _(report.grouped_coverage[group][:fallback]).must_be(:>, 0)
    end
    _(report.incomplete).wont_be_empty
    _(report.to_s).must_include("INCOMPLETE")
  end

  it "does not count empty results as materialized coverage" do
    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      model.source.dataset.delete
      model.dataset.delete
    end

    report = BlendParity.run(samples: 0)

    _(report.passed?).must_equal(false)
    _(report.failures).must_be_empty
    ["week", "month"].each do |group|
      _(report.grouped_coverage[group][:materialized]).must_equal(0)
      _(report.grouped_coverage[group][:fallback]).must_equal(0)
      _(report.grouped_coverage[group][:empty]).must_be(:>, 0)
    end
  end

  it "reports partial coverage separately from byte mismatches" do
    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      model.where(bucket_date: model.max(:bucket_date)).delete
    end

    report = BlendParity.run(samples: 0)

    _(report.passed?).must_equal(false)
    _(report.failures).must_be_empty
    ["week", "month"].each do |group|
      _(report.grouped_coverage[group][:materialized]).must_be(:>, 0)
      _(report.grouped_coverage[group][:fallback]).must_be(:>, 0)
    end
    _(report.incomplete).wont_be_empty
  end

  it "reports legitimately empty USD blends as unverified fallback" do
    [BlendedWeeklyRate, BlendedMonthlyRate].each do |model|
      bucket = model.source.max(:bucket_date)
      model.source.where(bucket_date: bucket).delete
      model.source.dataset.insert(bucket_date: bucket, provider: "ECB", base: "EUR", quote: "JPY", rate: 150.0)
      model.refresh([bucket])
    end

    report = BlendParity.run(samples: 0)

    _(report.passed?).must_equal(false)
    _(report.failures).must_be_empty
    _(report.incomplete).wont_be_empty
    _(report.to_s).must_include("legitimately empty USD blends")
  end
end
