# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/boja"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOJA do
      before do
        VCR.insert_cassette("boja", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BOJA.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 1))

        _(dataset).wont_be_empty
      end

      it "stores foreign currency as base and JMD as quote" do
        records = adapter.parse({
          "data" => [
            ["20 Mar 2026", "U.S. DOLLAR", "155.0000", "150.0000", "", "156.0000"],
          ],
        })

        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("JMD")
        _(records.first[:rate]).must_equal(152.5)
      end

      it "computes mid rate from sell and buy" do
        records = adapter.parse(
          "data" => [
            ["20 Mar 2026", "EURO", "186.2883", "176.9700", "156.9700", "191.1503"],
          ],
        )

        _(records.first[:base]).must_equal("EUR")
        _(records.first[:quote]).must_equal("JMD")
        _(records.first[:rate]).must_be_close_to(181.6292, 0.001)
      end

      it "skips unmapped currencies" do
        records = adapter.parse({
          "data" => [
            ["20 Mar 2026", "UNKNOWN CURRENCY", "1.0000", "0.9000", "", "1.1000"],
            ["20 Mar 2026", "U.S. DOLLAR", "155.0000", "150.0000", "", "156.0000"],
          ],
        })

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
      end

      it "handles rows with empty buy rate" do
        records = adapter.parse({
          "data" => [
            ["20 Mar 2026", "BELIZE DOLLAR", "74.9819", "67.4800", "", "77.1539"],
          ],
        })

        _(records.first[:base]).must_equal("BZD")
        _(records.first[:rate]).must_be_close_to(71.2310, 0.001)
      end
    end
  end
end
