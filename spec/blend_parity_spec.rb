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
    BlendedRate.rebuild
    report = BlendParity.run(samples: 30, seed: 20260723)

    _(report.failures).must_be_empty
    _(report.shapes).must_be(:>, 30)
  end
end
