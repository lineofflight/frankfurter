# frozen_string_literal: true

require_relative "../helper"
require "providers/bcra"

module Providers
  describe BCRA do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("bcra", match_requests_on: [:method, :host], allow_playback_repeats: true)
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { BCRA.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates" do
      provider.fetch(since: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 20)).import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "stores foreign currency as base and ARS as quote" do
      records = provider.parse({
        "results" => {
          "fecha" => "2026-03-20",
          "detalle" => [{
            "codigoMoneda" => "USD",
            "descripcion" => "DOLAR ESTADOUNIDENSE",
            "tipoPase" => 1,
            "tipoCotizacion" => "1075.0000",
          }],
        },
      })

      _(records.first[:base]).must_equal("USD")
      _(records.first[:quote]).must_equal("ARS")
      _(records.first[:rate]).must_equal(1075.0)
    end

    it "normalizes rates quoted per 1000 units" do
      records = provider.parse({
        "results" => {
          "fecha" => "2026-03-20",
          "detalle" => [{
            "codigoMoneda" => "VND",
            "descripcion" => "DONG VIETNAM (C/1.000 UNIDADES)",
            "tipoPase" => 0.038036,
            "tipoCotizacion" => "53.04096500",
          }],
        },
      })

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("VND")
      _(records.first[:rate]).must_be_close_to(0.05304, 0.001)
    end

    it "skips excluded codes" do
      records = provider.parse({
        "results" => {
          "fecha" => "2026-03-20",
          "detalle" => [
            { "codigoMoneda" => "REF", "tipoCotizacion" => "1.0" },
            { "codigoMoneda" => "VEB", "tipoCotizacion" => "1.0" },
            { "codigoMoneda" => "MXP", "tipoCotizacion" => "1.0" },
            { "codigoMoneda" => "USD", "tipoCotizacion" => "1075.0000" },
          ],
        },
      })

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
    end
  end
end
