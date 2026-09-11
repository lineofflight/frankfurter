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

      it "flags itself as a source that may revise published values" do
        _(HMRC.revises?).must_equal(true)
      end

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

      describe "publication lead" do
        # HMRC publishes next month's rates on the penultimate Thursday of this one. The row is dated the 1st it takes
        # effect, so it sits in the future for a week or two.
        it "fetches the following month's file as soon as it exists" do
          dataset = Date.stub(:today, Date.new(2026, 8, 20)) { adapter.fetch(after: Date.new(2026, 8, 1)) }

          _(dataset.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 8, 1), Date.new(2026, 9, 1)])
        end

        it "tolerates the following month not being published yet" do
          dataset = Date.stub(:today, Date.new(2026, 9, 11)) { adapter.fetch(after: Date.new(2026, 9, 1)) }

          _(dataset.map { |r| r[:date] }.uniq).must_equal([Date.new(2026, 9, 1)])
        end

        it "raises when a past month's file is missing" do
          _ do
            Date.stub(:today, Date.new(2021, 1, 15)) do
              adapter.fetch(after: Date.new(2020, 12, 1), upto: Date.new(2020, 12, 31))
            end
          end
            .must_raise(HTTP::StatusError)
        end
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

        it "maps HMRC's non-ISO labels to ISO codes" do
          csv = "#{header}Venezuela,Venezuelan Bolivar,VED,1047.3299,01/09/2026,30/09/2026\n" \
                "Zimbabwe,Zimbabwe Gold,ZIG,36.2052,01/09/2026,30/09/2026\n"
          quotes = adapter.parse(csv).map { |r| r[:quote] }

          _(quotes).must_equal(["VES", "ZWG"])
        end

        it "leaves the retired sucre code alone for validation to drop" do
          csv = "#{header}Ecuador,Dollar,ECS,1.3554,01/09/2026,30/09/2026\n" \
                "USA,Dollar,USD,1.3554,01/09/2026,30/09/2026\n"
          quotes = adapter.parse(csv).map { |r| r[:quote] }

          _(quotes).must_equal(["ECS", "USD"])
        end

        it "keeps an old-leone value labelled SLE under its real code" do
          # HMRC's November 2022 file labels 15631 leone per pound, an old-leone magnitude, as SLE. The new leone only
          # appears in HMRC's files from March 2023 at ~24 per pound.
          csv = "#{header}Sierra Leone,Leone,SLE,15631.1792,01/11/2022,30/11/2022\n" \
                "Sierra Leone,Leone,SLE,23.9705,01/03/2023,31/03/2023\n"
          records = adapter.parse(csv)

          _(records.map { |r| [r[:date], r[:quote]] })
            .must_equal([[Date.new(2022, 11, 1), "SLL"], [Date.new(2023, 3, 1), "SLE"]])
        end

        it "keeps a mid-month correction as a second observation" do
          csv = "#{header}USA,Dollar,USD,1.3554,01/10/2026,14/10/2026\n" \
                "USA,Dollar,USD,1.2900,15/10/2026,31/10/2026\n"
          records = adapter.parse(csv)

          _(records.map { |r| [r[:date], r[:rate]] })
            .must_equal([[Date.new(2026, 10, 1), 1.3554], [Date.new(2026, 10, 15), 1.29]])
        end

        it "raises when the expected columns are missing" do
          csv = "Country,Currency,Code,Rate,From,To\nUSA,Dollar,USD,1.3554,01/09/2026,30/09/2026\n"

          error = _ { adapter.parse(csv) }.must_raise(RuntimeError)
          _(error.message).must_include("HMRC")
        end

        it "parses a body that arrived without a UTF-8 tag" do
          csv = "#{header}USA,Dollar,USD,1.3554,01/09/2026,30/09/2026\n".b

          _(adapter.parse(csv).size).must_equal(1)
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
