# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcn"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCN do
      before do
        VCR.insert_cassette("bcn", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BCN.new }

      it "fetches rates" do
        skip "legacy TLS not configured" unless ENV["OPENSSL_CONF"]

        dataset = adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 20))

        _(dataset).wont_be_empty
        _(dataset.first[:base]).must_equal("USD")
        _(dataset.first[:quote]).must_equal("NIO")
        _(dataset.first[:rate]).must_be(:>, 0)
      end

      it "filters dates to requested range" do
        skip "legacy TLS not configured" unless ENV["OPENSSL_CONF"]

        dataset = adapter.fetch(after: Date.new(2026, 3, 17), upto: Date.new(2026, 3, 19))

        dates = dataset.map { |r| r[:date] }

        _(dates.min).must_be(:>=, Date.new(2026, 3, 17))
        _(dates.max).must_be(:<=, Date.new(2026, 3, 19))
      end

      it "parses response with correct structure" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              <RecuperaTC_MesResponse xmlns="http://servicios.bcn.gob.ni/">
                <RecuperaTC_MesResult>
                  <Detalle_TC xmlns="">
                    <Tc><Fecha>2026-03-18</Fecha><Valor>36.6243</Valor><Ano>2026</Ano><Mes>3</Mes><Dia>18</Dia></Tc>
                    <Tc><Fecha>2026-03-19</Fecha><Valor>36.6243</Valor><Ano>2026</Ano><Mes>3</Mes><Dia>19</Dia></Tc>
                  </Detalle_TC>
                </RecuperaTC_MesResult>
              </RecuperaTC_MesResponse>
            </soap:Body>
          </soap:Envelope>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(2)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 18))
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("NIO")
        _(records.first[:rate]).must_equal(36.6243)
      end

      it "skips entries with empty values" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              <RecuperaTC_MesResponse xmlns="http://servicios.bcn.gob.ni/">
                <RecuperaTC_MesResult>
                  <Detalle_TC xmlns="">
                    <Tc><Fecha>2026-03-18</Fecha><Valor>36.6243</Valor><Ano>2026</Ano><Mes>3</Mes><Dia>18</Dia></Tc>
                    <Tc><Fecha>2026-03-19</Fecha><Valor></Valor><Ano>2026</Ano><Mes>3</Mes><Dia>19</Dia></Tc>
                  </Detalle_TC>
                </RecuperaTC_MesResult>
              </RecuperaTC_MesResponse>
            </soap:Body>
          </soap:Envelope>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
      end

      it "skips entries with zero rates" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              <RecuperaTC_MesResponse xmlns="http://servicios.bcn.gob.ni/">
                <RecuperaTC_MesResult>
                  <Detalle_TC xmlns="">
                    <Tc><Fecha>2026-03-18</Fecha><Valor>0</Valor><Ano>2026</Ano><Mes>3</Mes><Dia>18</Dia></Tc>
                  </Detalle_TC>
                </RecuperaTC_MesResult>
              </RecuperaTC_MesResponse>
            </soap:Body>
          </soap:Envelope>
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end
    end
  end
end
