# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcu"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCU do
      before do
        VCR.insert_cassette("bcu")
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BCU.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 27), upto: Date.new(2026, 3, 31))

        _(dataset).wont_be_empty
      end

      it "fetches BCU rates with UYU quote" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 27), upto: Date.new(2026, 3, 31))
        record = dataset.first

        _(record[:quote]).must_equal("UYU")
      end

      it "fetches numeric rates greater than zero" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 27), upto: Date.new(2026, 3, 31))

        dataset.each do |record|
          _(record[:rate]).must_be_instance_of(Float)
          _(record[:rate]).must_be(:>, 0)
        end
      end

      it "fetches ISO currency codes" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 27), upto: Date.new(2026, 3, 31))

        dataset.each do |record|
          _(Money::Currency.find(record[:base])).wont_be_nil
        end
      end

      it "fetches valid dates" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 27), upto: Date.new(2026, 3, 31))

        dataset.each do |record|
          _(record[:date]).must_be_instance_of(Date)
        end
      end

      it "parses SOAP response with correct fields" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
            <SOAP-ENV:Body>
              <wsbcucotizaciones.ExecuteResponse xmlns="Cotiza">
                <Salida xmlns="Cotiza">
                  <respuestastatus>
                    <status>1</status>
                    <codigoerror>0</codigoerror>
                    <mensaje/>
                  </respuestastatus>
                  <datoscotizaciones>
                    <datoscotizaciones.dato xmlns="Cotiza">
                      <Fecha>2026-03-27</Fecha>
                      <Moneda>2225</Moneda>
                      <Nombre>DLS. USA BILLETE</Nombre>
                      <CodigoISO>DLS.</CodigoISO>
                      <Emisor>USA</Emisor>
                      <TCC>38.5</TCC>
                      <TCV>39.5</TCV>
                      <ArbAct>1.0</ArbAct>
                      <FormaArbitrar>0</FormaArbitrar>
                    </datoscotizaciones.dato>
                    <datoscotizaciones.dato xmlns="Cotiza">
                      <Fecha>2026-03-27</Fecha>
                      <Moneda>1111</Moneda>
                      <Nombre>EURO</Nombre>
                      <CodigoISO>EUR</CodigoISO>
                      <Emisor>ZONA EURO</Emisor>
                      <TCC>42.2</TCC>
                      <TCV>42.8</TCV>
                      <ArbAct>1.0</ArbAct>
                      <FormaArbitrar>0</FormaArbitrar>
                    </datoscotizaciones.dato>
                  </datoscotizaciones>
                </Salida>
              </wsbcucotizaciones.ExecuteResponse>
            </SOAP-ENV:Body>
          </SOAP-ENV:Envelope>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(2)
        _(records[0][:base]).must_equal("USD")
        _(records[0][:quote]).must_equal("UYU")
        _(records[0][:rate]).must_be_close_to(39.0, 0.01)
        _(records[0][:date]).must_equal(Date.new(2026, 3, 27))
        _(records[1][:base]).must_equal("EUR")
        _(records[1][:quote]).must_equal("UYU")
        _(records[1][:rate]).must_be_close_to(42.5, 0.01)
      end

      it "skips unknown currency codes" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
            <SOAP-ENV:Body>
              <wsbcucotizaciones.ExecuteResponse xmlns="Cotiza">
                <Salida xmlns="Cotiza">
                  <respuestastatus>
                    <status>1</status>
                    <codigoerror>0</codigoerror>
                    <mensaje/>
                  </respuestastatus>
                  <datoscotizaciones>
                    <datoscotizaciones.dato xmlns="Cotiza">
                      <Fecha>2026-03-27</Fecha>
                      <Moneda>2225</Moneda>
                      <Nombre>DLS. USA BILLETE</Nombre>
                      <CodigoISO>DLS.</CodigoISO>
                      <Emisor>USA</Emisor>
                      <TCC>38.5</TCC>
                      <TCV>39.5</TCV>
                      <ArbAct>1.0</ArbAct>
                      <FormaArbitrar>0</FormaArbitrar>
                    </datoscotizaciones.dato>
                    <datoscotizaciones.dato xmlns="Cotiza">
                      <Fecha>2026-03-27</Fecha>
                      <Moneda>9999</Moneda>
                      <Nombre>UNKNOWN</Nombre>
                      <CodigoISO>UNK</CodigoISO>
                      <Emisor>UNKNOWN</Emisor>
                      <TCC>1.0</TCC>
                      <TCV>1.5</TCV>
                      <ArbAct>1.0</ArbAct>
                      <FormaArbitrar>0</FormaArbitrar>
                    </datoscotizaciones.dato>
                  </datoscotizaciones>
                </Salida>
              </wsbcucotizaciones.ExecuteResponse>
            </SOAP-ENV:Body>
          </SOAP-ENV:Envelope>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
      end

      it "calculates midpoint rate from TCC and TCV" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
            <SOAP-ENV:Body>
              <wsbcucotizaciones.ExecuteResponse xmlns="Cotiza">
                <Salida xmlns="Cotiza">
                  <respuestastatus>
                    <status>1</status>
                    <codigoerror>0</codigoerror>
                    <mensaje/>
                  </respuestastatus>
                  <datoscotizaciones>
                    <datoscotizaciones.dato xmlns="Cotiza">
                      <Fecha>2026-03-27</Fecha>
                      <Moneda>2225</Moneda>
                      <Nombre>DLS. USA BILLETE</Nombre>
                      <CodigoISO>DLS.</CodigoISO>
                      <Emisor>USA</Emisor>
                      <TCC>38.0</TCC>
                      <TCV>40.0</TCV>
                      <ArbAct>1.0</ArbAct>
                      <FormaArbitrar>0</FormaArbitrar>
                    </datoscotizaciones.dato>
                  </datoscotizaciones>
                </Salida>
              </wsbcucotizaciones.ExecuteResponse>
            </SOAP-ENV:Body>
          </SOAP-ENV:Envelope>
        XML

        records = adapter.parse(xml)

        _(records.first[:rate]).must_equal(39.0)
      end

      it "skips UYU self-reference currency" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
            <SOAP-ENV:Body>
              <wsbcucotizaciones.ExecuteResponse xmlns="Cotiza">
                <Salida xmlns="Cotiza">
                  <respuestastatus>
                    <status>1</status>
                    <codigoerror>0</codigoerror>
                    <mensaje/>
                  </respuestastatus>
                  <datoscotizaciones>
                    <datoscotizaciones.dato xmlns="Cotiza">
                      <Fecha>2026-03-27</Fecha>
                      <Moneda>0</Moneda>
                      <Nombre>PESO URUGUAYO</Nombre>
                      <CodigoISO>UYU</CodigoISO>
                      <Emisor>URUGUAY</Emisor>
                      <TCC>1.0</TCC>
                      <TCV>1.0</TCV>
                      <ArbAct>1.0</ArbAct>
                      <FormaArbitrar>0</FormaArbitrar>
                    </datoscotizaciones.dato>
                  </datoscotizaciones>
                </Salida>
              </wsbcucotizaciones.ExecuteResponse>
            </SOAP-ENV:Body>
          </SOAP-ENV:Envelope>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(0)
      end
    end
  end
end
