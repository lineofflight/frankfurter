# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/hmrc"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe HMRC do
      before do
        VCR.insert_cassette("hmrc", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { HMRC.new }

      it "fetches monthly rates dated on their effective dates" do
        dataset = adapter.fetch(after: Date.new(2026, 8, 1), upto: Date.new(2026, 9, 1))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dataset.size).must_be(:>, 100)
        _(dates).must_equal([Date.new(2026, 8, 1), Date.new(2026, 9, 1)])
        _(dataset.all? { |r| r[:base] == "GBP" }).must_equal(true)
      end

      it "emits one row per pair and date" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 1), upto: Date.new(2026, 9, 1))
        quotes = dataset.map { |r| r[:quote] }

        _(quotes.uniq.size).must_equal(quotes.size)
      end

      describe "#parse" do
        let(:header) { "Country/Territories,Currency,Currency Code,Currency Units per £1,Start date,End date\n" }

        it "maps rates to records with GBP as base" do
          csv = "#{header}Eurozone,Euro,EUR,1.1681,01/09/2026,30/09/2026\n"
          records = adapter.parse(csv)

          _(records).must_equal([{ date: Date.new(2026, 9, 1), base: "GBP", quote: "EUR", rate: 1.1681 }])
        end

        it "deduplicates entries sharing a currency code" do
          csv = "#{header}Benin,CFA Franc,XOF,766.2127,01/09/2026,30/09/2026\n" \
                "Senegal,CFA Franc,XOF,766.2127,01/09/2026,30/09/2026\n"
          records = adapter.parse(csv)

          _(records.size).must_equal(1)
          _(records.first[:quote]).must_equal("XOF")
          _(records.first[:rate]).must_equal(766.2127)
        end

        it "maps code aliases to ISO equivalents" do
          csv = "#{header}Ecuador,Dollar,ECS,1.3554,01/09/2026,30/09/2026\n" \
                "Venezuela,Venezuelan Bolivar,VED,1047.3299,01/09/2026,30/09/2026\n" \
                "Zimbabwe,Zimbabwe Gold,ZIG,36.2052,01/09/2026,30/09/2026\n"
          records = adapter.parse(csv)
          quotes = records.map { |r| r[:quote] }

          _(quotes).must_include("USD")
          _(quotes).must_include("VES")
          _(quotes).must_include("ZWG")
        end

        it "drops rows with non-positive or unparseable rates" do
          csv = "#{header}Nowhere,Zero,XYZ,0.0,01/09/2026,30/09/2026\n" \
                "Nowhere,Negative,ABC,-1.5,01/09/2026,30/09/2026\n" \
                "Nowhere,Invalid,DEF,N/A,01/09/2026,30/09/2026\n"
          records = adapter.parse(csv)

          _(records).must_be_empty
        end
      end
    end
  end
end
