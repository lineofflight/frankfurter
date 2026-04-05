# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/banxico"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BANXICO do
      let(:adapter) { BANXICO.new }

      describe "with API key" do
        before do
          skip "BANXICO_API_KEY not set" unless ENV["BANXICO_API_KEY"]
          VCR.insert_cassette("banxico")
        end

        after { VCR.eject_cassette }

        it "fetches rates since a date" do
          dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 15))

          _(dataset).wont_be_empty
        end

        it "fetches multiple currencies per date" do
          dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 15))
          dates = dataset.map { |r| r[:date] }.uniq
          sample = dataset.select { |r| r[:date] == dates.first }

          _(sample.size).must_be(:>, 1)
        end
      end

      describe "parse" do
        it "parses series response" do
          json = {
            bmx: {
              series: [
                {
                  idSerie: "SF43718",
                  datos: [
                    { fecha: "15/03/2026", dato: "17.1234" },
                    { fecha: "16/03/2026", dato: "17.2345" },
                  ],
                },
                {
                  idSerie: "SF46410",
                  datos: [
                    { fecha: "15/03/2026", dato: "18.5678" },
                  ],
                },
              ],
            },
          }.to_json

          rates = adapter.parse(json)

          _(rates.length).must_equal(3)
          _(rates[0][:base]).must_equal("USD")
          _(rates[0][:quote]).must_equal("MXN")
          _(rates[0][:rate]).must_be_close_to(17.1234)
          _(rates[2][:base]).must_equal("EUR")
        end

        it "handles comma-formatted numbers" do
          json = {
            bmx: {
              series: [
                {
                  idSerie: "SF43718",
                  datos: [{ fecha: "15/03/2026", dato: "17,123.45" }],
                },
              ],
            },
          }.to_json

          rates = adapter.parse(json)

          _(rates[0][:rate]).must_be_close_to(17_123.45)
        end

        it "raises on invalid values" do
          json = {
            bmx: {
              series: [
                {
                  idSerie: "SF43718",
                  datos: [{ fecha: "15/03/2026", dato: "N/E" }],
                },
              ],
            },
          }.to_json

          _(-> { adapter.parse(json) }).must_raise(ArgumentError)
        end
      end
    end
  end
end
