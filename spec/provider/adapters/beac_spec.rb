# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/beac"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BEAC do
      before do
        VCR.insert_cassette("beac", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BEAC.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 21), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "stores foreign currency as base and XAF as quote" do
        html = <<~HTML
          <div class="document">
            <div class="taux_de_change" >
              <span class="code_valeur">USD/XAF</span>
              <div id="middle">561.2317</div>
              <div id="right">566.0505</div>
            </div>
          </div>
          <p>Date de valeur : 22/05/2026</p>
        HTML

        records = adapter.parse(html)

        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("XAF")
        _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "averages buy and sell into a mid rate" do
        html = <<~HTML
          <span class="code_valeur">USD/XAF</span>
          <div id="middle">561.2317</div>
          <div id="right">566.0505</div>
          <p>Date de valeur : 22/05/2026</p>
        HTML

        records = adapter.parse(html)

        _(records.first[:rate]).must_be_within_delta(563.6411, 0.0001)
      end

      it "publishes EUR/XAF at the fixed peg" do
        html = <<~HTML
          <span class="code_valeur">EUR/XAF</span>
          <div id="middle">655.957</div>
          <div id="right">655.957</div>
          <p>Date de valeur : 22/05/2026</p>
        HTML

        records = adapter.parse(html)

        _(records.first[:base]).must_equal("EUR")
        _(records.first[:rate]).must_equal(655.957)
      end

      it "parses all 13 pairs from the widget" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 21), upto: Date.new(2026, 5, 22))

        pairs = dataset.map { |r| [r[:base], r[:quote]] }

        _(pairs).must_include(["EUR", "XAF"])
        _(pairs).must_include(["USD", "XAF"])
        _(pairs.size).must_equal(13)
      end

      it "returns empty when widget date is missing" do
        records = adapter.parse("<html><body>no widget</body></html>")

        _(records).must_be_empty
      end
    end
  end
end
