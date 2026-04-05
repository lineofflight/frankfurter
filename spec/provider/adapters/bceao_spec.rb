# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bceao"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCEAO do
      before do
        VCR.insert_cassette("bceao", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BCEAO.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 20))

        _(dataset).wont_be_empty
      end

      it "stores foreign currency as base and XOF as quote" do
        html = <<~HTML
          <h2>Cours des devises du jeudi 20 mars 2026</h2>
          <table><tbody>
          <tr><th>Devise</th><th>CFA</th></tr>
          <tr><td>Dollar us</td><td>605,5200</td></tr>
          </tbody></table>
        HTML

        records = adapter.parse(html, date: Date.new(2026, 3, 20))

        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("XOF")
        _(records.first[:rate]).must_equal(605.52)
      end

      it "parses rates with thousands separator" do
        html = <<~HTML
          <h2>Cours des devises du jeudi 20 mars 2026</h2>
          <table><tbody>
          <tr><th>Devise</th><th>CFA</th></tr>
          <tr><td>Dinar Koweitien</td><td>1.953,0200</td></tr>
          </tbody></table>
        HTML

        records = adapter.parse(html, date: Date.new(2026, 3, 20))

        _(records.first[:base]).must_equal("KWD")
        _(records.first[:rate]).must_equal(1953.02)
      end

      it "returns empty for weekend responses" do
        html = "<h2>Cours des devises du saturday 22 mars 2026</h2>"

        records = adapter.parse(html, date: Date.new(2026, 3, 22))

        _(records).must_be_empty
      end
    end
  end
end
