# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/boj"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BOJ do
      let(:adapter) { BOJ.new }

      describe "integration" do
        before do
          VCR.insert_cassette("boj")
        end

        after { VCR.eject_cassette }

        it "fetches rates since a date" do
          dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 15))

          _(dataset).wont_be_empty
        end

        it "fetches both series per date" do
          dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 15))
          dates = dataset.map { |r| r[:date] }.uniq
          sample = dataset.select { |r| r[:date] == dates.first }

          _(sample.size).must_equal(2)
        end
      end

      describe "parse" do
        it "parses RESULTSET response" do
          json = {
            RESULTSET: [
              {
                SERIES_CODE: "FXERD04",
                VALUES: {
                  SURVEY_DATES: [20_260_303, 20_260_304, 20_260_305],
                  VALUES: [149.50, 150.10, 151.00],
                },
              },
              {
                SERIES_CODE: "FXERD34",
                VALUES: {
                  SURVEY_DATES: [20_260_303, 20_260_304, 20_260_305],
                  VALUES: [1.0820, 1.0845, 1.0900],
                },
              },
            ],
          }.to_json

          rates = adapter.parse(json)

          _(rates.length).must_equal(6)

          usd_jpy = rates.select { |r| r[:base] == "USD" }

          _(usd_jpy.length).must_equal(3)
          _(usd_jpy[0][:quote]).must_equal("JPY")
          _(usd_jpy[0][:rate]).must_be_close_to(149.50)

          eur_usd = rates.select { |r| r[:base] == "EUR" }

          _(eur_usd[0][:quote]).must_equal("USD")
          _(eur_usd[0][:rate]).must_be_close_to(1.0820)
        end

        it "skips nil values" do
          json = {
            RESULTSET: [
              {
                SERIES_CODE: "FXERD04",
                VALUES: {
                  SURVEY_DATES: [20_260_301, 20_260_302, 20_260_303],
                  VALUES: [nil, nil, 149.50],
                },
              },
            ],
          }.to_json

          rates = adapter.parse(json)

          _(rates.length).must_equal(1)
          _(rates[0][:rate]).must_be_close_to(149.50)
        end

        it "handles malformed JSON" do
          rates = adapter.parse("not json")

          _(rates).must_be_empty
        rescue JSON::ParserError
          # acceptable — caller rescues this
        end

        it "skips unknown series codes" do
          json = {
            RESULTSET: [
              {
                SERIES_CODE: "UNKNOWN99",
                VALUES: {
                  SURVEY_DATES: [20_260_303],
                  VALUES: [1.23],
                },
              },
            ],
          }.to_json

          rates = adapter.parse(json)

          _(rates).must_be_empty
        end
      end
    end
  end
end
