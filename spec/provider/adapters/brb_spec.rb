# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/brb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BRB do
      before do
        VCR.insert_cassette("brb", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BRB.new }

      it "fetches rates with BIF as the quote currency" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["BIF"])
      end

      it "covers all 19 quote currencies in the bulletin" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 22), upto: Date.new(2026, 5, 22))
        bases = dataset.map { |r| r[:base] }.uniq.sort

        _(bases.size).must_equal(19)
        _(bases).must_include("USD")
        _(bases).must_include("EUR")
        _(bases).must_include("KES")
      end

      it "maps DTS to XDR" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 22), upto: Date.new(2026, 5, 22))
        xdr = dataset.find { |r| r[:base] == "XDR" }

        _(xdr).wont_be_nil
        _(xdr[:rate]).must_be_close_to(4086.83, 1.0)
      end

      it "emits the mid rate (Cours moyen jour), not buy or sell" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 22), upto: Date.new(2026, 5, 22))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(2990.36, 0.01)
      end

      it "filters records by the requested date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.min).must_be(:>=, Date.new(2026, 5, 20))
        _(dates.max).must_be(:<=, Date.new(2026, 5, 22))
      end

      it "returns USD/BIF in a plausible range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 22), upto: Date.new(2026, 5, 22))
        usd = dataset.find { |r| r[:base] == "USD" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(2990.0, 500.0)
      end

      it "parses asterisk-marked currencies the same as plain rows" do
        # KES carries the asterisk; the rate is still authoritative.
        dataset = adapter.fetch(after: Date.new(2026, 5, 22), upto: Date.new(2026, 5, 22))
        kes = dataset.find { |r| r[:base] == "KES" }

        _(kes).wont_be_nil
        _(kes[:rate]).must_be_close_to(23.06, 0.05)
      end

      it "skips dates whose PDF body is empty or not a PDF" do
        # Some archive dates return a zero-byte body. The adapter must treat the
        # date as missing rather than letting PDF::Reader raise on bad input.
        VCR.eject_cassette

        index_body = <<~HTML
          <a href="/sites/default/files/2026-05/Cours%20de%20change%20du%2021-05-2026.pdf">empty</a>
        HTML

        begin
          VCR.turned_off do
            WebMock.stub_request(:get, %r{https://www\.brb\.bi/en/affichagetoustauxchange})
              .to_return(
                { status: 200, body: index_body, headers: { "Content-Type" => "text/html; charset=UTF-8" } },
                { status: 200, body: "", headers: { "Content-Type" => "text/html; charset=UTF-8" } },
              )
            WebMock.stub_request(:get, %r{/sites/default/files/2026-05/Cours%20de%20change%20du%2021-05-2026\.pdf})
              .to_return(status: 200, body: "", headers: { "Content-Type" => "application/pdf" })

            result = adapter.fetch(after: Date.new(2026, 5, 21), upto: Date.new(2026, 5, 21))

            _(result).must_equal([])
          end
        ensure
          WebMock.reset!
          VCR.insert_cassette("brb", match_requests_on: [:method, :uri])
        end
      end
    end
  end
end
