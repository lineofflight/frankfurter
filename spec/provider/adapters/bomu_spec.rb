# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bomu"

class Provider
  module Adapters
    describe BOMU do
      before { VCR.insert_cassette("bomu") }
      after { VCR.eject_cassette }

      let(:adapter) { BOMU.new }

      def page(*rows)
        <<~HTML
          <div class="view-display-id-page">
            <div class="view-content"><div class="table-responsive"><table><thead><tr><th>Country</th><th>Code</th><th>T.T</th><th>D.D</th>
            <th>Notes</th><th>T.T/D.D</th><th>Notes</th><th>Date</th></tr></thead>
            <tbody>#{rows.join}</tbody></table></div></div>
          </div>
        HTML
      end

      def row(code: "USD 1", buy: "28.9431", sell: "29.3634", date: "03-07-2001")
        <<~HTML
          <tr class="tblConso">
            <td class="views-field-name">U.S.A.</td>
            <td class="views-field-field-currency">#{code}</td>
            <td class="views-field-php">#{buy}</td>
            <td class="views-field-php-1">28.8117</td><td class="views-field-php-2">28.7649</td>
            <td class="views-field-php-3">#{sell}</td><td class="views-field-php-4">.0000</td>
            <td class="views-field-field-transaction-date">#{date}</td>
          </tr>
        HTML
      end

      it "fetches a bounded recent archive with native direction" do
        records = adapter.fetch(after: Date.new(2026, 9, 23), upto: Date.new(2026, 9, 25))

        _(records.length).must_equal(36)
        _(records.map { |r| r[:date] }.uniq.sort).must_equal((Date.new(2026, 9, 23)..Date.new(2026, 9, 25)).to_a)
        _(records.map { |r| r[:quote] }.uniq).must_equal(["MUR"])
        aud = records.find { |r| r[:base] == "AUD" && r[:date] == Date.new(2026, 9, 25) }

        _(aud[:rate]).must_equal(34.28315)
      end

      it "fetches historical transfer quotes" do
        records = adapter.fetch(after: Date.new(2005, 1, 4), upto: Date.new(2005, 1, 4))
        usd = records.find { |r| r[:base] == "USD" }

        _(records.length).must_equal(10)
        _(usd[:date]).must_equal(Date.new(2005, 1, 4))
        _(usd[:rate]).must_equal(28.00555)
      end

      it "chunks longer ranges and keeps both date bounds inclusive" do
        [["04-01-2005", "03-02-2005", ["03-01-2005", "04-01-2005", "03-02-2005"]],
         ["04-02-2005", "04-02-2005", ["04-02-2005", "05-02-2005"]],].each do |first, last, dates|
          WebMock.stub_request(:get, BOMU::URL).with(query: {
            "field_transaction_date_value[value][date]" => first,
            "field_transaction_date_value_1[value][date]" => last,
          }).to_return(body: page(*dates.map { |date| row(date:) }))
        end

        records = adapter.fetch(after: Date.new(2005, 1, 4), upto: Date.new(2005, 2, 4))

        _(records.map { |r| r[:date] }).must_equal([Date.new(2005, 1, 4), Date.new(2005, 2, 3), Date.new(2005, 2, 4)])
      end

      it "uses transfer buy and sell prices without float noise" do
        record = adapter.parse(page(row)).first

        _(record).must_equal(date: Date.new(2001, 7, 3), base: "USD", quote: "MUR", rate: 29.15325,
                             bid: 28.9431, ask: 29.3634, mid: nil,)
      end

      it "normalizes per 100 yen including source components" do
        record = adapter.parse(page(row(code: "JPY 100", buy: "26.9610", sell: "27.8600"))).first

        _(record[:base]).must_equal("JPY")
        _(record[:rate]).must_equal(0.274105)
        _(record[:bid]).must_equal(0.26961)
        _(record[:ask]).must_equal(0.2786)
      end

      it "removes the source NUL before parsing the rates table" do
        _(adapter.parse("<nav>Menu\0</nav>#{page(row)}").length).must_equal(1)
      end

      it "ignores the duplicate attachment and current sidebar tables" do
        attachment = page(row).sub("view-display-id-page", "view-display-id-attachment_1")
        html = page(row).sub(%r{</div>\s*\z}, "#{attachment}</div>")

        _(adapter.parse(html).length).must_equal(1)
      end

      it "skips incomplete nonpositive and malformed quotes" do
        html = page(row(buy: ""), row(sell: "N/A"), row(buy: "0"), row(sell: "-1"),
                    row(code: "JPY 0"), row(code: "Not a currency"),)

        _(adapter.parse(html)).must_be_empty
      end

      it "accepts an empty filtered view on a nonpublishing day" do
        _(adapter.parse('<div class="view-display-id-page"></div>')).must_be_empty
      end

      it "raises if the expected view is missing" do
        _(-> { adapter.parse("<html>Request rejected</html>") }).must_raise(RuntimeError)
      end
    end
  end
end
