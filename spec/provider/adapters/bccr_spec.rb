# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bccr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCCR do
      before do
        VCR.insert_cassette("bccr", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BCCR.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24))

        _(dataset).wont_be_empty
      end

      it "parses response with correct structure" do
        records = adapter.parse({
          "columnas" => [
            { "field" => "nombre", "tituloIngles" => "Indicators" },
            { "field" => "serie1", "tituloIngles" => "20 mar 2026" },
            { "field" => "serie2", "tituloIngles" => "21 mar 2026" },
          ],
          "indicadoresRaiz" => [
            {
              "idIndicador" => 317,
              "nombreIngles" => "Buy",
              "series" => { "serie1Ingles" => "463.24", "serie2Ingles" => "463.50" },
            },
            {
              "idIndicador" => 318,
              "nombreIngles" => "Sell",
              "series" => { "serie1Ingles" => "469.49", "serie2Ingles" => "468.29" },
            },
          ],
        })

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("CRC")
        _(records.first[:rate]).must_equal(469.49)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 20))
      end

      it "skips empty series values" do
        records = adapter.parse({
          "columnas" => [
            { "field" => "nombre", "tituloIngles" => "Indicators" },
            { "field" => "serie1", "tituloIngles" => "20 mar 2026" },
            { "field" => "serie2", "tituloIngles" => "21 mar 2026" },
          ],
          "indicadoresRaiz" => [
            {
              "idIndicador" => 318,
              "nombreIngles" => "Sell",
              "series" => { "serie1Ingles" => "469.49", "serie2Ingles" => "" },
            },
          ],
        })

        _(records.length).must_equal(1)
      end
    end
  end
end
