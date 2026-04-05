# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/rba"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe RBA do
      before do
        VCR.insert_cassette("rba", match_requests_on: [:method, :host])
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { RBA.new }

      it "fetches rates" do
        dataset = adapter.fetch(after: Date.new(2025, 1, 1))

        _(dataset).wont_be_empty
      end

      it "stores AUD as base and foreign currency as quote" do
        records = adapter.parse(<<~CSV)
          F11.1  EXCHANGE RATES
          Title,A$1=USD,Trade-weighted Index May 1970 = 100,A$1=JPY
          Description,AUD/USD Exchange Rate,Australian Dollar Trade-weighted Index,AUD/JPY Exchange Rate
          Frequency,Daily,Daily,Daily
          Type,Indicative,Indicative,Indicative
          Units,USD,Index,JPY


          Source,WM/Reuters,RBA,RBA
          Publication date,20-Mar-2026,20-Mar-2026,20-Mar-2026
          Series ID,FXRUSD,FXRTWI,FXRJY
          03-Jan-2023,0.6828,61.40,88.48
        CSV

        _(records.length).must_equal(2)
        _(records.first[:base]).must_equal("AUD")
        _(records.first[:quote]).must_equal("USD")
        _(records.first[:rate]).must_equal(0.6828)
        _(records.last[:quote]).must_equal("JPY")
        _(records.last[:rate]).must_equal(88.48)
      end
    end
  end
end
