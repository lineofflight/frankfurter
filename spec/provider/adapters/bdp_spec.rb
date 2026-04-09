# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bdp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BDP do
      before do
        VCR.insert_cassette("bdp", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BDP.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 1), upto: Date.new(1998, 12, 31))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 1), upto: Date.new(1998, 12, 31))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "filters by date range" do
        dataset = adapter.fetch(after: Date.new(1998, 12, 28), upto: Date.new(1998, 12, 31))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dates.first).must_be(:>=, Date.new(1998, 12, 28))
        _(dates.last).must_be(:<=, Date.new(1998, 12, 31))
      end

      it "parses JSON-stat with correct base and quote" do
        data = {
          "dimension" => {
            "12" => { "category" => { "index" => ["826"], "label" => { "826" => "United States" } } },
            "reference_date" => { "category" => { "index" => ["1998-12-31"] } },
          },
          "extension" => {
            "series" => [
              { "id" => 180121, "label" => "USD", "dimension_category" => [{ "dimension_id" => 12, "category_id" => 826 }] },
            ],
          },
          "value" => [171.829],
        }

        records = adapter.parse(data)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("PTE")
        _(records.first[:rate]).must_equal(171.829)
        _(records.first[:date]).must_equal(Date.new(1998, 12, 31))
      end

      it "skips null values" do
        data = {
          "dimension" => {
            "12" => { "category" => { "index" => ["826"] } },
            "reference_date" => { "category" => { "index" => ["1998-12-30", "1998-12-31"] } },
          },
          "extension" => {
            "series" => [
              { "id" => 180121, "dimension_category" => [{ "dimension_id" => 12, "category_id" => 826 }] },
            ],
          },
          "value" => [nil, 171.829],
        }

        records = adapter.parse(data)

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(1998, 12, 31))
      end
    end
  end
end
