# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/ust"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe UST do
      before do
        VCR.insert_cassette("ust", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { UST.new }

      it "fetches a quarter's rates dated on their effective dates, from the start date inclusive" do
        dataset = adapter.fetch(after: Date.new(2026, 6, 30), upto: Date.new(2026, 8, 31))
        dates = dataset.map { |r| r[:date] }.uniq.sort

        _(dataset.size).must_be(:>, 100)
        _(dates.first).must_equal(Date.new(2026, 6, 30))
        _(dataset.all? { |r| r[:base] == "USD" }).must_equal(true)
      end

      it "keeps mid-quarter amendments as their own rows" do
        dataset = adapter.fetch(after: Date.new(2026, 6, 30), upto: Date.new(2026, 8, 31))
        krw = dataset.select { |r| r[:quote] == "KRW" }.sort_by { |r| r[:date] }

        _(krw.map { |r| r[:date] }).must_equal([Date.new(2026, 6, 30), Date.new(2026, 8, 31)])
        _(krw.last[:rate]).must_equal(1367.46)
      end

      it "emits one row per pair and date" do
        dataset = adapter.fetch(after: Date.new(2026, 6, 30), upto: Date.new(2026, 8, 31))
        keys = dataset.map { |r| [r[:date], r[:quote]] }

        _(keys.uniq.size).must_equal(keys.size)
      end

      describe "#parse" do
        def row(label, rate, record: "2026-06-30", effective: record)
          { "record_date" => record, "effective_date" => effective, "country_currency_desc" => label,
            "exchange_rate" => rate, }
        end

        it "maps labels to ISO codes with USD as base" do
          records = adapter.parse([row("Norway-Krone", "9.916")])

          _(records).must_equal([{ date: Date.new(2026, 6, 30), base: "USD", quote: "NOK", rate: 9.916 }])
        end

        it "drops labels it does not map" do
          records = adapter.parse([row("Cross Border-Euro", "0.877"), row("Ecuador-Dolares", "1.0")])

          _(records).must_be_empty
        end

        it "lets the first listed label win a shared pair" do
          records = adapter.parse([row("Togo-Cfa Franc", "570.0"), row("Benin-Cfa Franc", "571.5")])

          _(records.size).must_equal(1)
          _(records.first[:rate]).must_equal(571.5)
        end

        it "keeps a predecessor code before the unit change and drops the label after it" do
          old = adapter.parse([row("Turkey-Lira", "1418000.0", record: "2004-06-30")])
          late = adapter.parse([row("Turkey-Lira", "5.755", record: "2019-06-30")])

          _(old.first[:quote]).must_equal("TRL")
          _(late).must_be_empty
        end

        it "switches code at the source's own switch date" do
          before = adapter.parse([row("Mauritania-Ouguiya", "355.0", record: "2018-03-31")])
          after = adapter.parse([row("Mauritania-Ouguiya", "35.5", record: "2018-06-30")])

          _(before.first[:quote]).must_equal("MRO")
          _(after.first[:quote]).must_equal("MRU")
        end

        it "dates amendments on their effective date" do
          records = adapter.parse([row("Korea-Won", "1367.46", record: "2026-06-30", effective: "2026-08-31")])

          _(records.first[:date]).must_equal(Date.new(2026, 8, 31))
        end
      end
    end
  end
end
