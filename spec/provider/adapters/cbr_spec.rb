# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbr"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBR do
      before do
        VCR.insert_cassette("cbr", match_requests_on: [:method, :uri], allow_playback_repeats: true)
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBR.new }

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end

      it "fetches foreign currency as base and RUB as quote" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))
        usd = dataset.find { |r| r[:base] == "USD" && r[:quote] == "RUB" }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be(:>, 50)
      end

      it "fetches XAU, XAG, XPT and XPD against RUB" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 5))
        metals = dataset.select { |r| CBR::METAL_CODES.value?(r[:base]) }
        bases = metals.map { |r| r[:base] }.uniq

        _(bases).must_include("XAU")
        _(bases).must_include("XAG")
        _(bases).must_include("XPT")
        _(bases).must_include("XPD")
        metals.each { |r| _(r[:quote]).must_equal("RUB") }
      end

      it "normalizes metal rates from RUB-per-gram to RUB-per-troy-ounce" do
        xml = <<~XML
          <?xml version="1.0" encoding="windows-1251"?>
          <Metall FromDate="20260424" ToDate="20260424" name="Precious metals quotations">
            <Record Date="24.04.2026" Code="1"><Buy>11409,47</Buy><Sell>11409,47</Sell></Record>
            <Record Date="24.04.2026" Code="2"><Buy>187,82</Buy><Sell>187,82</Sell></Record>
            <Record Date="24.04.2026" Code="3"><Buy>5009,28</Buy><Sell>5009,28</Sell></Record>
            <Record Date="24.04.2026" Code="4"><Buy>3750,95</Buy><Sell>3750,95</Sell></Record>
          </Metall>
        XML

        records = adapter.parse_metals(xml)
        xau = records.find { |r| r[:base] == "XAU" }

        _(xau[:rate]).must_be_close_to(11409.47 * Adapter::GRAMS_PER_TROY_OUNCE, 0.0001)
        _(records.size).must_equal(4)
      end

      it "skips weekend metal records" do
        xml = <<~XML
          <?xml version="1.0" encoding="windows-1251"?>
          <Metall FromDate="20260425" ToDate="20260426" name="Precious metals quotations">
            <Record Date="25.04.2026" Code="1"><Buy>11409,47</Buy><Sell>11409,47</Sell></Record>
            <Record Date="26.04.2026" Code="1"><Buy>11409,47</Buy><Sell>11409,47</Sell></Record>
          </Metall>
        XML

        _(adapter.parse_metals(xml)).must_be_empty
      end
    end
  end
end
