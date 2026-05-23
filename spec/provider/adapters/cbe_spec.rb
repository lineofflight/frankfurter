# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbe"
require "zip"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBE do
      before do
        VCR.insert_cassette("cbe", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBE.new }

      def build_xlsx(rows, shared_strings: nil)
        # shared_strings layout: [ "" (0), "Date" (1), "Currency" (2), "Buy" (3), "Sell" (4), <currency names...> ]
        defaults = ["", "Date", "Currency", "Buy", "Sell"]
        shared_strings ||= defaults + rows.map { |r| r[:currency_name] }.uniq

        sheet_rows = rows.map.with_index do |row, idx|
          r = idx + 3
          currency_index = shared_strings.index(row[:currency_name])
          <<~ROW
            <row r="#{r}"><c r="A#{r}" s="4"><v>#{row[:serial]}</v></c><c r="B#{r}" s="5" t="s"><v>#{currency_index}</v></c><c r="C#{r}" s="6"><v>#{row[:buy]}</v></c><c r="D#{r}" s="6"><v>#{row[:sell]}</v></c></row>
          ROW
        end.join

        sheet_xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
            <sheetData>
              <row r="1"><c r="A1" s="2" t="s"><v>0</v></c></row>
              <row r="2"><c r="A2" s="3" t="s"><v>1</v></c><c r="B2" s="3" t="s"><v>2</v></c><c r="C2" s="3" t="s"><v>3</v></c><c r="D2" s="3" t="s"><v>4</v></c></row>
              #{sheet_rows}
            </sheetData>
          </worksheet>
        XML

        ss_items = shared_strings.map { |s| "<si><t>#{s}</t></si>" }.join
        ss_xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="#{shared_strings.size}" uniqueCount="#{shared_strings.size}">#{ss_items}</sst>
        XML

        buffer = Zip::OutputStream.write_buffer do |zip|
          zip.put_next_entry("xl/worksheets/sheet1.xml")
          zip.write(sheet_xml)
          zip.put_next_entry("xl/sharedStrings.xml")
          zip.write(ss_xml)
        end
        buffer.string
      end

      it "parses USD rows with foreign base and EGP quote" do
        xlsx = build_xlsx([
          { serial: 46142, currency_name: "US Dollar", buy: 53.5501, sell: 53.6894 },
        ])

        records = adapter.parse(xlsx)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("EGP")
        _(records.first[:date]).must_equal(Date.new(2026, 4, 30))
        # mid = (53.5501 + 53.6894) / 2
        _(records.first[:rate]).must_be_close_to(53.61975, 0.0001)
      end

      it "coerces buy and sell to a mid rate" do
        xlsx = build_xlsx([
          { serial: 46142, currency_name: "Euro", buy: 60.0, sell: 61.0 },
        ])

        records = adapter.parse(xlsx)

        _(records.first[:rate]).must_be_close_to(60.5, 0.0001)
      end

      it "normalizes JPY per-100 quotes" do
        xlsx = build_xlsx([
          { serial: 46142, currency_name: "Japanese Yen 100", buy: 34.0, sell: 34.2 },
        ])

        records = adapter.parse(xlsx)

        _(records.first[:base]).must_equal("JPY")
        # mid = 34.1, divided by 100 → 0.341
        _(records.first[:rate]).must_be_close_to(0.341, 0.0001)
      end

      it "maps all 18 currency names to ISO codes" do
        rows = CBE::CURRENCIES.keys.map.with_index do |name, idx|
          { serial: 46142 - idx, currency_name: name, buy: 10.0, sell: 11.0 }
        end
        xlsx = build_xlsx(rows)

        records = adapter.parse(xlsx)

        _(records.map { |r| r[:base] }.sort).must_equal([
          "AED",
          "AUD",
          "BHD",
          "CAD",
          "CHF",
          "CNY",
          "DKK",
          "EUR",
          "GBP",
          "JOD",
          "JPY",
          "KWD",
          "NOK",
          "OMR",
          "QAR",
          "SAR",
          "SEK",
          "USD",
        ])
        _(records.map { |r| r[:quote] }.uniq).must_equal(["EGP"])
      end

      it "skips unknown currency names" do
        xlsx = build_xlsx([
          { serial: 46142, currency_name: "Mystery Coin", buy: 1.0, sell: 1.0 },
        ])

        _(adapter.parse(xlsx)).must_be_empty
      end

      it "converts Excel serial dates to Date" do
        xlsx = build_xlsx([
          { serial: 45292, currency_name: "US Dollar", buy: 30.0, sell: 30.5 },
        ])

        records = adapter.parse(xlsx)

        # Excel epoch 1899-12-30; 45292 -> 2024-01-01
        _(records.first[:date]).must_equal(Date.new(2024, 1, 1))
      end

      it "fetches rates for a date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 9))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["EGP"])
      end

      it "returns USD/EGP within a plausible post-float range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 9))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 4, 1) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(53.0, 5.0)
      end
    end
  end
end
