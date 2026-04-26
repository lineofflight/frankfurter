# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bdp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BDP do
      before do
        VCR.insert_cassette("bdp", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BDP.new }

      it "parses JSON-stat with foreign base and PTE quote" do
        json = {
          "size" => [1, 1, 1, 1, 1, 1, 1, 2],
          "value" => [184.244, 185.487],
          "extension" => {
            "series" => [
              {
                "id" => 180121,
                "label" => "US, Dollars (USD) against Escudo - daily",
                "dimension_category" => [{ "dimension_id" => 12, "category_id" => 826 }],
              },
            ],
          },
          "dimension" => {
            "12" => { "category" => { "index" => ["826"] } },
            "reference_date" => { "category" => { "index" => ["1998-01-02", "1998-01-05"] } },
          },
        }

        records = adapter.parse(json)

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("PTE")
        _(records.first[:rate]).must_equal(184.244)
        _(records.first[:date]).must_equal(Date.new(1998, 1, 2))
      end

      it "interleaves multiple counterparties using counterparty index ordering" do
        json = {
          "value" => [184.244, 185.487, 288.532, 288.186],
          "extension" => {
            "series" => [
              {
                "label" => "US, Dollars (USD) against Escudo - daily",
                "dimension_category" => [{ "dimension_id" => 12, "category_id" => 826 }],
              },
              {
                "label" => "United Kingdom, Pounds (GBP) against Escudo - daily",
                "dimension_category" => [{ "dimension_id" => 12, "category_id" => 824 }],
              },
            ],
          },
          "dimension" => {
            "12" => { "category" => { "index" => ["826", "824"] } },
            "reference_date" => { "category" => { "index" => ["1998-12-30", "1998-12-31"] } },
          },
        }

        records = adapter.parse(json)
        usd = records.select { |r| r[:base] == "USD" }
        gbp = records.select { |r| r[:base] == "GBP" }

        _(usd.map { |r| r[:rate] }).must_equal([184.244, 185.487])
        _(gbp.map { |r| r[:rate] }).must_equal([288.532, 288.186])
      end

      it "skips null observations" do
        json = {
          "value" => [184.244, nil],
          "extension" => {
            "series" => [
              {
                "label" => "US, Dollars (USD) against Escudo - daily",
                "dimension_category" => [{ "dimension_id" => 12, "category_id" => 826 }],
              },
            ],
          },
          "dimension" => {
            "12" => { "category" => { "index" => ["826"] } },
            "reference_date" => { "category" => { "index" => ["1998-01-02", "1998-01-03"] } },
          },
        }

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(1998, 1, 2))
      end

      it "remaps the European Currency Unit label from ECU to ISO 4217 XEU" do
        json = {
          "value" => [122.93],
          "extension" => {
            "series" => [
              {
                "label" => "ECU - Banco do Portugal (ECU) against Escudo - daily",
                "dimension_category" => [{ "dimension_id" => 12, "category_id" => 2777 }],
              },
            ],
          },
          "dimension" => {
            "12" => { "category" => { "index" => ["2777"] } },
            "reference_date" => { "category" => { "index" => ["1998-12-31"] } },
          },
        }

        records = adapter.parse(json)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("XEU")
      end

      it "skips series whose label has no parenthesised ISO code" do
        # BdP labels usually carry a code, but if a series ever omits it we should
        # drop it cleanly rather than emit malformed records.
        json = {
          "value" => [42.0],
          "extension" => {
            "series" => [
              {
                "label" => "Some unlabelled series against Escudo - daily",
                "dimension_category" => [{ "dimension_id" => 12, "category_id" => 999 }],
              },
            ],
          },
          "dimension" => {
            "12" => { "category" => { "index" => ["999"] } },
            "reference_date" => { "category" => { "index" => ["1998-01-02"] } },
          },
        }

        _(adapter.parse(json)).must_be_empty
      end

      it "returns an empty dataset when the response has no series" do
        json = { "size" => [0], "value" => [], "extension" => { "series" => [] }, "dimension" => {} }

        _(adapter.parse(json)).must_be_empty
      end

      it "fetches rates for a historical date range" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 28), upto: Date.new(1998, 12, 31))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["PTE"])
      end

      it "fetches multiple currencies per date and follows pagination" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 28), upto: Date.new(1998, 12, 31))
        bases = dataset.map { |r| r[:base] }.uniq

        # Pagination across all source-filtered series must surface BEF/ATS (page 3) too.
        _(bases).must_include("USD")
        _(bases).must_include("ATS")
      end

      it "returns USD/PTE in a plausible range for late-1998" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 28), upto: Date.new(1998, 12, 31))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(1998, 12, 30) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(171.0, 5.0)
      end
    end
  end
end
