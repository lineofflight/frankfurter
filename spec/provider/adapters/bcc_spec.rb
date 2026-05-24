# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcc"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCC do
      before do
        VCR.insert_cassette("bcc", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { BCC.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "covers multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 19), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses tasaEspecial as the headline rate" do
        json = JSON.dump([
          { "fecha" => "2026-05-22", "tasaOficial" => 24, "tasaPublica" => 120, "tasaEspecial" => 507 },
        ])
        records = adapter.parse(json, "USD")

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("CUP")
        _(records.first[:rate]).must_equal(507.0)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "skips entries with nil tasaEspecial" do
        json = JSON.dump([
          { "fecha" => "2026-05-22", "tasaOficial" => 24, "tasaPublica" => 120, "tasaEspecial" => nil },
        ])
        records = adapter.parse(json, "USD")

        _(records).must_be_empty
      end

      it "skips zero rates" do
        json = JSON.dump([
          { "fecha" => "2026-05-22", "tasaOficial" => 24, "tasaPublica" => 120, "tasaEspecial" => 0 },
        ])
        records = adapter.parse(json, "USD")

        _(records).must_be_empty
      end

      it "handles empty response" do
        _(adapter.parse("[]", "USD")).must_be_empty
      end
    end
  end
end
