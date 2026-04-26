# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbm"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBM do
      before do
        VCR.insert_cassette("nbm", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBM.new }

      it "fetches rates for a date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 6), upto: Date.new(2026, 4, 8))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 6), upto: Date.new(2026, 4, 8))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first && r[:base] != "XAU" && r[:base] != "XAG" }

        _(sample.size).must_be(:>, 1)
      end

      it "fetches foreign currency as base and MDL as quote" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 6), upto: Date.new(2026, 4, 8))
        usd = dataset.find { |r| r[:base] == "USD" && r[:quote] == "MDL" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be(:>, 10)
      end

      it "fetches XAU and XAG against MDL" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 6), upto: Date.new(2026, 4, 8))
        metals = dataset.select { |r| NBM::METAL_CODES.include?(r[:base]) }
        bases = metals.map { |r| r[:base] }.uniq

        _(bases).must_include("XAU")
        _(bases).must_include("XAG")
        metals.each { |r| _(r[:quote]).must_equal("MDL") }
      end

      it "normalizes metal rates from MDL-per-gram to MDL-per-troy-ounce" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <MetalPrice Date="24.04.2026" name="The price of precious metals">
            <Metal ID="">
              <NumCode>961</NumCode>
              <CharCode>XAG</CharCode>
              <Nominal>1</Nominal>
              <Name>Gram silver</Name>
              <Value>41.5550</Value>
            </Metal>
            <Metal ID="">
              <NumCode>959</NumCode>
              <CharCode>XAU</CharCode>
              <Nominal>1</Nominal>
              <Name>Gram gold</Name>
              <Value>2619.6183</Value>
            </Metal>
          </MetalPrice>
        XML

        records = adapter.parse_metals(xml)
        xau = records.find { |r| r[:base] == "XAU" }
        xag = records.find { |r| r[:base] == "XAG" }

        _(records.size).must_equal(2)
        _(xau[:rate]).must_be_close_to(2619.6183 * Adapter::GRAMS_PER_TROY_OUNCE, 0.0001)
        _(xag[:rate]).must_be_close_to(41.5550 * Adapter::GRAMS_PER_TROY_OUNCE, 0.0001)
        _(xau[:quote]).must_equal("MDL")
      end

      it "skips weekend metal records" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <MetalPrice Date="25.04.2026" name="The price of precious metals">
            <Metal ID="">
              <NumCode>959</NumCode>
              <CharCode>XAU</CharCode>
              <Nominal>1</Nominal>
              <Name>Gram gold</Name>
              <Value>2619.6183</Value>
            </Metal>
          </MetalPrice>
        XML

        _(adapter.parse_metals(xml)).must_be_empty
      end

      it "parses XML correctly" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <ValCurs Date="08.04.2026" name="Official exchange rate">
            <Valute ID="44">
              <NumCode>840</NumCode>
              <CharCode>USD</CharCode>
              <Nominal>1</Nominal>
              <Name>US Dollar</Name>
              <Value>17.4597</Value>
            </Valute>
          </ValCurs>
        XML
        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("MDL")
        _(records.first[:rate]).must_be_close_to(17.4597, 0.0001)
      end

      it "normalizes rate by nominal" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <ValCurs Date="08.04.2026" name="Official exchange rate">
            <Valute ID="25">
              <NumCode>392</NumCode>
              <CharCode>JPY</CharCode>
              <Nominal>100</Nominal>
              <Name>Japanese Yen</Name>
              <Value>10.9277</Value>
            </Valute>
          </ValCurs>
        XML
        records = adapter.parse(xml)

        _(records.first[:rate]).must_be_close_to(0.109277, 0.0001)
      end

      it "skips zero values" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <ValCurs Date="01.01.1994" name="Official exchange rate">
            <Valute ID="43">
              <NumCode>804</NumCode>
              <CharCode>UAK</CharCode>
              <Nominal>0</Nominal>
              <Name>Ukrainian Karbovanets</Name>
              <Value>0.0000</Value>
            </Valute>
          </ValCurs>
        XML
        records = adapter.parse(xml)

        _(records).must_be_empty
      end

      it "skips invalid currency codes" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <ValCurs Date="08.04.2026" name="Official exchange rate">
            <Valute ID="99">
              <NumCode>999</NumCode>
              <CharCode>XX</CharCode>
              <Nominal>1</Nominal>
              <Name>Invalid</Name>
              <Value>1.5</Value>
            </Valute>
          </ValCurs>
        XML
        records = adapter.parse(xml)

        _(records).must_be_empty
      end
    end
  end
end
