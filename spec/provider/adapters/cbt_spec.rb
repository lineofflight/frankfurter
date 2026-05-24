# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbt"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBT do
      before do
        VCR.insert_cassette("cbt", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBT.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 22))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 22))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses rate entries with correct base and quote" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <cbt_currency_rate name="CBT Currency XML">
            <date>22.05.2026</date>
            <rate code="USD">
              <name>ABS-NYN DOLLARY</name>
              <rate_usd>1</rate_usd>
              <multiplier>1</multiplier>
              <rate_tmt>3.5</rate_tmt>
            </rate>
          </cbt_currency_rate>
        XML

        records = adapter.parse(xml)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("TMT")
        _(records.first[:rate]).must_be_close_to(3.5, 0.0001)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "normalizes rate by multiplier" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <cbt_currency_rate name="CBT Currency XML">
            <date>22.05.2026</date>
            <rate code="RUB">
              <name>RUS RUBLY</name>
              <rate_usd>70.9</rate_usd>
              <multiplier>100</multiplier>
              <rate_tmt>4.9365</rate_tmt>
            </rate>
          </cbt_currency_rate>
        XML

        records = adapter.parse(xml)

        _(records.first[:rate]).must_be_close_to(0.049365, 0.000001)
      end

      it "parses ISO date format from older archives" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <cbt_currency_rate name="CBT Currency XML">
            <date>2020-04-17</date>
            <rate code="USD">
              <name>ABS-NYN DOLLARY</name>
              <rate_usd>1</rate_usd>
              <multiplier>1</multiplier>
              <rate_tmt>3.5</rate_tmt>
            </rate>
          </cbt_currency_rate>
        XML

        records = adapter.parse(xml)

        _(records.first[:date]).must_equal(Date.new(2020, 4, 17))
      end

      it "skips zero rates" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <cbt_currency_rate name="CBT Currency XML">
            <date>22.05.2026</date>
            <rate code="USD">
              <name>ABS-NYN DOLLARY</name>
              <rate_usd>1</rate_usd>
              <multiplier>1</multiplier>
              <rate_tmt>0</rate_tmt>
            </rate>
          </cbt_currency_rate>
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end

      it "skips invalid currency codes" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <cbt_currency_rate name="CBT Currency XML">
            <date>22.05.2026</date>
            <rate code="XX">
              <name>Invalid</name>
              <rate_usd>1</rate_usd>
              <multiplier>1</multiplier>
              <rate_tmt>1.5</rate_tmt>
            </rate>
          </cbt_currency_rate>
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end

      it "skips when response date does not match requested date" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <cbt_currency_rate name="CBT Currency XML">
            <date>22.05.2026</date>
            <rate code="USD">
              <name>ABS-NYN DOLLARY</name>
              <rate_usd>1</rate_usd>
              <multiplier>1</multiplier>
              <rate_tmt>3.5</rate_tmt>
            </rate>
          </cbt_currency_rate>
        XML

        records = adapter.parse(xml, expected_date: Date.new(2020, 1, 1))

        _(records).must_be_empty
      end

      it "handles empty root" do
        xml = <<~XML
          <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
          <cbt_currency_rate name="CBT Currency XML">
            <date>22.05.2026</date>
          </cbt_currency_rate>
        XML

        records = adapter.parse(xml)

        _(records).must_be_empty
      end
    end
  end
end
