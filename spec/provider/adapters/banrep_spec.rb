# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/banrep"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BANREP do
      before do
        VCR.insert_cassette("banrep", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BANREP.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24))

        _(dataset).wont_be_empty
      end

      it "parses response with correct structure" do
        records = adapter.parse([
          {
            "valor" => "4181.69",
            "unidad" => "COP",
            "vigenciadesde" => "2026-03-24T00:00:00.000",
            "vigenciahasta" => "2026-03-24T00:00:00.000",
          },
          {
            "valor" => "4150.32",
            "unidad" => "COP",
            "vigenciadesde" => "2026-03-25T00:00:00.000",
            "vigenciahasta" => "2026-03-25T00:00:00.000",
          },
        ])

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("COP")
        _(records.first[:rate]).must_equal(4181.69)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 24))
      end

      it "skips records with missing values" do
        records = adapter.parse([
          {
            "valor" => "4181.69",
            "unidad" => "COP",
            "vigenciadesde" => "2026-03-24T00:00:00.000",
            "vigenciahasta" => "2026-03-24T00:00:00.000",
          },
          {
            "unidad" => "COP",
            "vigenciadesde" => "2026-03-25T00:00:00.000",
          },
        ])

        _(records.length).must_equal(1)
      end
    end
  end
end
