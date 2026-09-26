# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bfm"

class Provider
  module Adapters
    describe BFM do
      before { VCR.insert_cassette("bfm", match_requests_on: [:method, :uri, :body]) }
      after { VCR.eject_cassette }

      let(:adapter) { BFM.new }

      def response(rates, **extra)
        { code: "cours-de-mid-en-ar-filter", data: { status: 200, data: { coursMid: rates, **extra } } }.to_json
      end

      it "fetches all published currencies from the recent archive" do
        records = adapter.fetch(after: Date.new(2026, 9, 21), upto: Date.new(2026, 9, 23))

        _(records.length).must_equal(57)
        _(records.map { |r| r[:date] }.uniq.sort).must_equal((Date.new(2026, 9, 21)..Date.new(2026, 9, 23)).to_a)
        codes = ["AUD", "CAD", "CHF", "CNY", "DJF", "DKK", "EUR", "GBP", "HKD", "INR", "JPY", "MUR",
                 "NOK", "NZD", "SEK", "SGD", "USD", "XDR", "ZAR",]

        _(records.map { |r| r[:base] }.uniq.sort).must_equal(codes)
        _(records.map { |r| r[:quote] }.uniq).must_equal(["MGA"])
        usd = records.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 9, 23) }

        _(usd[:rate]).must_equal(BigDecimal("4391.53"))
      end

      it "fetches the earliest archive in native ariary units" do
        records = adapter.fetch(after: Date.new(2018, 1, 2), upto: Date.new(2018, 1, 5))
        usd = records.find { |r| r[:base] == "USD" && r[:date] == Date.new(2018, 1, 2) }

        _(records.length).must_equal(76)
        _(usd[:rate]).must_equal(BigDecimal("3220.45"))
        _(usd[:quote]).must_equal("MGA")
      end

      it "bounds each request to a year and keeps the requested date limits" do
        windows = [["2025/01/01", "2025/12/31", "2025-01-01"], ["2026/01/01", "2026/01/02", "2026-01-02"]]
        BFM::CURRENCIES.each do |code|
          windows.each do |first, last, date|
            WebMock.stub_request(:post, BFM::URL).with(body: {
              dateFilterDebut: first, dateFilterFin: last, filterData: code,
            }).to_return(body: response({ "2024-12-31" => "100", date => "123,45", "2026-01-03" => "200" }))
          end
        end

        records = adapter.fetch(after: Date.new(2025, 1, 1), upto: Date.new(2026, 1, 2))

        _(records.length).must_equal(38)
        _(records.map { |r| r[:date] }.uniq.sort).must_equal([Date.new(2025, 1, 1), Date.new(2026, 1, 2)])
      end

      it "relays the published reference without averaging daily extremes" do
        json = response({ "2018-01-02" => "3 220,45" },
                        coursMidMin: { "2018-01-02" => "3 210,00" }, coursMidMax: { "2018-01-02" => "3 252,00" },)

        expected = { date: Date.new(2018, 1, 2), base: "USD", quote: "MGA", rate: BigDecimal("3220.45") }

        _(adapter.parse(json, "USD")).must_equal([expected])
      end

      it "preserves long published digits through ingestion normalization" do
        record = adapter.parse(response({ "2026-09-23" => "4 391,53123456789" }), "USD").first

        _(RateComponents.attributes(record)[:mid]).must_equal(BigDecimal("4391.53123456789"))
      end

      it "accepts nonbreaking French grouping spaces without rescaling yen" do
        records = adapter.parse(response({ "2026-09-22" => "1\u00a0234,56", "2026-09-23" => "1\u202f234,57" }), "JPY")

        _(records.map { |r| r[:rate] }).must_equal([BigDecimal("1234.56"), BigDecimal("1234.57")])
      end

      it "skips missing invalid and nonpositive values" do
        json = response({ "2026-09-21" => nil, "2026-09-22" => "", "2026-09-23" => "N/A", "2026-09-24" => "0",
                          "2026-09-25" => "-1", })

        _(adapter.parse(json, "USD")).must_be_empty
      end

      it "accepts the explicit empty archive response" do
        _(adapter.parse(response([]), "USD")).must_be_empty
      end

      it "raises on semantic API errors or missing rate data" do
        ["{}", '{"data":{"status":500,"data":{"coursMid":[]}}}', response(["unexpected"])].each do |json|
          error = _(-> { adapter.parse(json, "USD") }).must_raise(RuntimeError)
          _(error.message).must_match(/BFM/)
        end
      end
    end
  end
end
