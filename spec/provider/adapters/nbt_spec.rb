# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbt"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBT do
      before do
        VCR.insert_cassette("nbt", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBT.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 20))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 20))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses Valute records with correct base and quote" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8" ?>
          <ValCurs Date="2026-05-20" name="Foreign Currency Market">
          <Valute ID="840">
             <CharCode>USD</CharCode>
             <Nominal>1</Nominal>
             <Name>US Dollar</Name>
             <Value>9.3288</Value>
          </Valute>
          </ValCurs>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("TJS")
        _(records.first[:rate]).must_be_close_to(9.3288, 0.0001)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 20))
      end

      it "normalizes rate by nominal" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8" ?>
          <ValCurs Date="2026-05-20" name="Foreign Currency Market">
          <Valute ID="860">
             <CharCode>UZS</CharCode>
             <Nominal>100</Nominal>
             <Name>Uzbekistan Sum</Name>
             <Value>0.0774</Value>
          </Valute>
          </ValCurs>
        XML

        records = adapter.parse(xml)

        _(records.first[:rate]).must_be_close_to(0.000774, 0.000001)
      end

      it "trusts CharCode over numeric ID" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8" ?>
          <ValCurs Date="2026-05-20" name="Foreign Currency Market">
          <Valute ID="810">
             <CharCode>RUB</CharCode>
             <Nominal>1</Nominal>
             <Name>Russian Ruble</Name>
             <Value>0.1308</Value>
          </Valute>
          </ValCurs>
        XML

        records = adapter.parse(xml)

        _(records.first[:base]).must_equal("RUB")
      end

      it "skips zero values" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8" ?>
          <ValCurs Date="2026-05-20" name="Foreign Currency Market">
          <Valute ID="840">
             <CharCode>USD</CharCode>
             <Nominal>1</Nominal>
             <Name>US Dollar</Name>
             <Value>0</Value>
          </Valute>
          </ValCurs>
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end

      it "skips invalid currency codes" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8" ?>
          <ValCurs Date="2026-05-20" name="Foreign Currency Market">
          <Valute ID="999">
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

      it "skips when response date does not match requested date" do
        # Out-of-range requests silently return today's snapshot
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8" ?>
          <ValCurs Date="2026-05-25" name="Foreign Currency Market">
          <Valute ID="840">
             <CharCode>USD</CharCode>
             <Nominal>1</Nominal>
             <Name>US Dollar</Name>
             <Value>9.2793</Value>
          </Valute>
          </ValCurs>
        XML

        records = adapter.parse(xml, expected_date: Date.new(2000, 1, 1))

        _(records).must_be_empty
      end

      it "handles empty ValCurs" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8" ?>
          <ValCurs Date="2026-05-20" name="Foreign Currency Market" />
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end
    end
  end
end
