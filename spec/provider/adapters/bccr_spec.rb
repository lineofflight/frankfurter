# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bccr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCCR do
      before do
        VCR.insert_cassette("bccr", match_requests_on: [:method, :host], allow_playback_repeats: true)
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BCCR.new }

      let(:csv) do
        "﻿US Dollar purchase and sale rates\r\n" \
          "Banco Central de Costa Rica Reference Rate, in Costa Rican currency (colones)\r\n" \
          "\r\n" \
          "\r\n" \
          "Date;Sales Exchange Rate\r\n" \
          "2026-03-20;469.49000000\r\n" \
          "2026-03-21;\r\n" \
          "2026-03-22;468.29000000\r\n"
      end

      it "fetches every date in the range" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24))

        _(dataset.map { |r| r[:date] }).must_equal((Date.new(2026, 3, 16)..Date.new(2026, 3, 24)).to_a)
        _(dataset.map { |r| [r[:base], r[:quote]] }.uniq).must_equal([["USD", "CRC"]])
      end

      it "parses response with correct structure" do
        records = adapter.parse(csv)

        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("CRC")
        _(records.first[:rate]).must_equal(469.49)
        _(records.first[:date]).must_equal(Date.new(2026, 3, 20))
      end

      it "skips empty series values" do
        records = adapter.parse(csv)

        _(records.map { |r| r[:date] }).must_equal([Date.new(2026, 3, 20), Date.new(2026, 3, 22)])
      end

      it "raises when the CSV header is missing" do
        _ { adapter.parse("Date;Purchase Exchange Rate\r\n2026-03-20;463.24000000\r\n") }.must_raise(RuntimeError)
      end
    end
  end
end
