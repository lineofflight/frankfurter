# frozen_string_literal: true

require_relative "../helper"
require "providers/nb"

module Providers
  describe NB do
    let(:provider) { NB.new }

    it "parses SDMX CSV data" do
      csv = <<~CSV
        FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
        B,USD,NOK,SP,2025-03-24,10.5432,0
        B,EUR,NOK,SP,2025-03-24,11.4567,0
        B,JPY,NOK,SP,2025-03-24,7.0123,2
      CSV

      records = provider.parse(csv)

      _(records.size).must_equal(3)

      usd = records.find { |r| r[:base] == "USD" }
      _(usd[:quote]).must_equal("NOK")
      _(usd[:rate]).must_be_close_to(10.5432)
      _(usd[:date]).must_equal(Date.new(2025, 3, 24))
      _(usd[:provider]).must_equal("NB")

      # JPY has UNIT_MULT=2, so rate is per 100 units
      jpy = records.find { |r| r[:base] == "JPY" }
      _(jpy[:rate]).must_be_close_to(0.070123)
    end

    it "skips non-business-day rows" do
      csv = <<~CSV
        FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
        M,USD,NOK,SP,2025-03,10.5432,0
      CSV

      records = provider.parse(csv)
      _(records).must_be_empty
    end

    it "skips invalid currency codes" do
      csv = <<~CSV
        FREQ,BASE_CUR,QUOTE_CUR,TENOR,TIME_PERIOD,OBS_VALUE,UNIT_MULT
        B,US,NOK,SP,2025-03-24,10.5432,0
      CSV

      records = provider.parse(csv)
      _(records).must_be_empty
    end
  end
end
