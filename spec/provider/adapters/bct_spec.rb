# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bct"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCT do
      before do
        VCR.insert_cassette("bct", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BCT.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 20))

        _(dataset).wont_be_empty

        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates).must_equal([Date.new(2026, 5, 18), Date.new(2026, 5, 19), Date.new(2026, 5, 20)])
      end

      it "stores foreign currency as base and TND as quote" do
        html = <<~HTML
          <h3 class='bct-mod-hdr' id='1'>Cours Moyens des Devises Cotées</h3>
          <h5>Journée du 20/05/2026</h5>
          <table>
            <tr>
              <td>DOLLAR DES USA</td>
              <td>USD</td>
              <td><div align='right'>1</div></td>
              <td><div align='right'>  2,9048</div></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("TND")
        _(records.first[:rate]).must_be_close_to(2.9048, 0.0001)
      end

      it "normalizes per-1000 rates (JPY)" do
        html = <<~HTML
          <h3>Cours Moyens des Devises Cotées</h3>
          <h5>Journée du 20/05/2026</h5>
          <table>
            <tr>
              <td>YEN JAPONAIS</td>
              <td>JPY</td>
              <td><div align='right'>1000</div></td>
              <td><div align='right'> 18,4230</div></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.first[:base]).must_equal("JPY")
        _(records.first[:rate]).must_be_close_to(0.01842, 0.00001)
      end

      it "normalizes per-100 and per-10 rates" do
        html = <<~HTML
          <h3>Cours Moyens des Devises Cotées</h3>
          <h5>Journée du 20/05/2026</h5>
          <table>
            <tr>
              <td>COURONNE DANOISE</td>
              <td>DKK</td>
              <td><div align='right'>100</div></td>
              <td><div align='right'> 45,4665</div></td>
            </tr>
            <tr>
              <td>DIRHAM DES EAU</td>
              <td>AED</td>
              <td><div align='right'>10</div></td>
              <td><div align='right'>  7,9781</div></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        dkk = records.find { |r| r[:base] == "DKK" }
        aed = records.find { |r| r[:base] == "AED" }

        _(dkk[:rate]).must_be_close_to(0.454665, 0.000001)
        _(aed[:rate]).must_be_close_to(0.79781, 0.00001)
      end

      it "ignores the manual-exchange table below the first table" do
        html = <<~HTML
          <h3>Cours Moyens des Devises Cotées</h3>
          <h5>Journée du 20/05/2026</h5>
          <table>
            <tr>
              <td>DOLLAR DES USA</td>
              <td>USD</td>
              <td><div align='right'>1</div></td>
              <td><div align='right'>  2,9048</div></td>
            </tr>
          </table>
          <!-- 2eme module-->
          <table>
            <tr>
              <td>DOLLAR DES USA</td>
              <td>USD</td>
              <td><div align='right'>1</div></td>
              <td><div align='right'>  2,924</div></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        usd = records.select { |r| r[:base] == "USD" }

        _(usd.length).must_equal(1)
        _(usd.first[:rate]).must_be_close_to(2.9048, 0.0001)
      end

      it "rejects responses whose echoed date does not match the request" do
        html = <<~HTML
          <h3>Cours Moyens des Devises Cotées</h3>
          <h5>Journée du 18/05/2026</h5>
          <table>
            <tr>
              <td>DOLLAR DES USA</td>
              <td>USD</td>
              <td><div align='right'>1</div></td>
              <td><div align='right'>  2,9048</div></td>
            </tr>
          </table>
        HTML

        records = adapter.parse(html, date: Date.new(2026, 5, 23))

        _(records).must_be_empty
      end

      it "returns empty when the response has no echoed date (Exhausted Resultset)" do
        html = "<h3>Cours Moyens des Devises Cotées</h3>Un problème rencontré!!!\nExhausted Resultset"

        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records).must_be_empty
      end
    end
  end
end
