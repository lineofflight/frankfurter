# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbk"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBK do
      before do
        VCR.insert_cassette("nbk", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBK.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 4, 1), upto: Date.new(2026, 4, 3))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses XML correctly" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <rates>
            <date>01.04.2026</date>
            <item>
              <fullname>ДОЛЛАР США</fullname>
              <title>USD</title>
              <description>460.37</description>
              <quant>1</quant>
              <index>DOWN</index>
              <change>-5.96</change>
            </item>
          </rates>
        XML
        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("KZT")
        _(records.first[:rate]).must_be_close_to(460.37, 0.01)
        _(records.first[:date]).must_equal(Date.new(2026, 4, 1))
      end

      it "normalizes rate by quantity" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <rates>
            <date>01.04.2026</date>
            <item>
              <fullname>ВОНА ЮЖНО-КОРЕЙСКАЯ</fullname>
              <title>KRW</title>
              <description>30.69</description>
              <quant>100</quant>
              <index>DOWN</index>
              <change>-0.32</change>
            </item>
          </rates>
        XML
        records = adapter.parse(xml)

        _(records.first[:rate]).must_be_close_to(0.3069, 0.0001)
      end

      it "skips zero rates" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <rates>
            <date>01.04.2026</date>
            <item>
              <fullname>ДОЛЛАР США</fullname>
              <title>USD</title>
              <description>0.0</description>
              <quant>1</quant>
              <index>DOWN</index>
              <change>0</change>
            </item>
          </rates>
        XML
        records = adapter.parse(xml)

        _(records).must_be_empty
      end

      it "skips invalid currency codes" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <rates>
            <date>01.04.2026</date>
            <item>
              <fullname>Invalid</fullname>
              <title>XX</title>
              <description>1.5</description>
              <quant>1</quant>
              <index>DOWN</index>
              <change>0</change>
            </item>
          </rates>
        XML
        records = adapter.parse(xml)

        _(records).must_be_empty
      end

      it "handles empty response" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8"?>
          <rates>
          </rates>
        XML
        records = adapter.parse(xml)

        _(records).must_be_empty
      end
    end
  end
end
