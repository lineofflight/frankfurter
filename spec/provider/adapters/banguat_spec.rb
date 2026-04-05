# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/banguat"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe Banguat do
      before do
        Provider.dataset.insert_conflict.insert(key: "Banguat", name: "Banco de Guatemala")
        VCR.insert_cassette("banguat", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { Banguat.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 20))

        _(dataset).wont_be_empty
      end

      it "fetches GTQ rates with USD base" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 20))
        record = dataset.first

        _(record[:base]).must_equal("USD")
        _(record[:quote]).must_equal("GTQ")
      end

      it "parses XML with correct fields" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              <TipoCambioRangoResponse xmlns="http://www.banguat.gob.gt/variables/ws/">
                <TipoCambioRangoResult>
                  <Vars>
                    <Var>
                      <moneda>2</moneda>
                      <fecha>10/03/2026</fecha>
                      <venta>7.65857</venta>
                      <compra>7.65857</compra>
                    </Var>
                  </Vars>
                </TipoCambioRangoResult>
              </TipoCambioRangoResponse>
            </soap:Body>
          </soap:Envelope>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("GTQ")
        _(records.first[:rate]).must_equal(7.65857)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 10))
      end
    end
  end
end
