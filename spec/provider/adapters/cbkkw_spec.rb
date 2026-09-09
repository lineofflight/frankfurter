# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbkkw"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBKKW do
      before do
        VCR.insert_cassette("cbkkw", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBKKW.new }

      def fragment(rows)
        body = rows.map { |date, fils| "<tr><td>#{date}</td><td>#{fils}</td></tr>" }.join("\n")
        <<~HTML
          <div class="tab-content">
          <script type="application/json" id="currencyJSON">{"dateMin": "02/01/2008","dataSet": []}</script>
          <table class="table table-bordered table-striped ctable">
            <thead class="thead-dark">
              <tr class="heading-row"><th>Date</th><th>KWD / US Dollar</th></tr>
            </thead>
            <tbody>
              #{body}
            </tbody>
          </table>
          </div>
        HTML
      end

      it "parses fils per unit into KWD with foreign base" do
        records = adapter.parse(fragment([["08.09.2026", "306.650"]]), "USD")

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2026, 9, 8))
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("KWD")
        _(records.first[:rate]).must_equal(0.30665)
      end

      it "keeps sub-fils quotes exact" do
        records = adapter.parse(fragment([["09.09.2026", "0.018"]]), "IDR")

        _(records.first[:rate]).must_equal(0.000018)
      end

      it "skips zero rates" do
        _(adapter.parse(fragment([["09.09.2026", "0.000"]]), "VEF")).must_be_empty
      end

      it "returns nothing for an empty table" do
        _(adapter.parse(fragment([]), "USD")).must_be_empty
      end

      it "raises on a body without the lookup fragment" do
        _ { adapter.parse("<html><body>Request Rejected</body></html>", "USD") }.must_raise(RuntimeError)
      end

      it "parses the form id and currency ids from the lookup page" do
        html = <<~HTML
          <input id="formId" name="formId" type="hidden" value="127906" />
          <select id="selCurrency" name="selCurrency">
            <option value="" selected>Select</option>
            <option value="USD:128735"  selected>US Dollar</option>
            <option value="EUR:128674" >EURO</option>
          </select>
        HTML

        form_id, currencies = adapter.parse_form(html)

        _(form_id).must_equal("127906")
        _(currencies).must_equal({ "USD" => "128735", "EUR" => "128674" })
      end

      it "raises when the lookup page has no currency options" do
        _ { adapter.parse_form('<input name="formId" value="1" />') }.must_raise(RuntimeError)
      end

      it "fetches rates for a date range" do
        dataset = adapter.fetch(after: Date.new(2026, 9, 6), upto: Date.new(2026, 9, 8))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["KWD"])
        _(dataset.map { |r| r[:base] }.uniq.size).must_be(:>, 100)
        _(dataset.map { |r| r[:date] }.uniq.sort)
          .must_equal([Date.new(2026, 9, 6), Date.new(2026, 9, 7), Date.new(2026, 9, 8)])

        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 9, 8) }

        _(usd[:rate]).must_equal(0.30665)
      end
    end
  end
end
