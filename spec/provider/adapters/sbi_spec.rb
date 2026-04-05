# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/sbi"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe SBI do
      before do
        VCR.insert_cassette("sbi", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { SBI.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 24), upto: Date.new(2026, 3, 28))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 24), upto: Date.new(2026, 3, 28))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "parses XML with correct base and quote" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <Group ID="9">
            <TimeSeries ID="4055">
              <Name>Bandaríkjadalur</Name>
              <TimeSeriesData>
                <Entry><Date>3/24/2026 12:00:00 AM</Date><Value>124.440000</Value></Entry>
              </TimeSeriesData>
            </TimeSeries>
          </Group>
        XML

        records = adapter.parse(xml, SBI::GROUP9_CURRENCIES)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("ISK")
        _(records.first[:rate]).must_equal(124.44)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 24))
      end

      it "parses GroupID=7 currencies" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <Group ID="7">
            <TimeSeries ID="29">
              <Name>Kínverskt júan</Name>
              <TimeSeriesData>
                <Entry><Date>3/25/2026 12:00:00 AM</Date><Value>17.930000</Value></Entry>
              </TimeSeriesData>
            </TimeSeries>
          </Group>
        XML

        records = adapter.parse(xml, SBI::GROUP7_CURRENCIES)

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("CNY")
        _(records.first[:quote]).must_equal("ISK")
        _(records.first[:rate]).must_equal(17.93)
      end

      it "skips entries with missing values" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <Group ID="9">
            <TimeSeries ID="4055">
              <Name>Bandaríkjadalur</Name>
              <TimeSeriesData>
                <Entry><Date>3/24/2026 12:00:00 AM</Date></Entry>
              </TimeSeriesData>
            </TimeSeries>
          </Group>
        XML

        records = adapter.parse(xml, SBI::GROUP9_CURRENCIES)

        _(records).must_be_empty
      end

      it "skips entries with zero rates" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <Group ID="9">
            <TimeSeries ID="4055">
              <Name>Bandaríkjadalur</Name>
              <TimeSeriesData>
                <Entry><Date>3/24/2026 12:00:00 AM</Date><Value>0.000000</Value></Entry>
              </TimeSeriesData>
            </TimeSeries>
          </Group>
        XML

        records = adapter.parse(xml, SBI::GROUP9_CURRENCIES)

        _(records).must_be_empty
      end

      it "skips unknown TimeSeries IDs" do
        xml = <<~XML
          <?xml version="1.0" encoding="utf-8"?>
          <Group ID="9">
            <TimeSeries ID="9999">
              <Name>Unknown</Name>
              <TimeSeriesData>
                <Entry><Date>3/24/2026 12:00:00 AM</Date><Value>100.000000</Value></Entry>
              </TimeSeriesData>
            </TimeSeries>
          </Group>
        XML

        records = adapter.parse(xml, SBI::GROUP9_CURRENCIES)

        _(records).must_be_empty
      end
    end
  end
end
