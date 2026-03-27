# frozen_string_literal: true

require_relative "../helper"
require "providers/rba"

module Providers
  describe RBA do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("rba", match_requests_on: [:method, :host])
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { RBA.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates" do
      provider.fetch(since: Date.new(2025, 1, 1)).import

      _(count_unique_dates).must_be(:>, 1)
    end

    it "stores AUD as base and foreign currency as quote" do
      records = provider.parse(<<~CSV)
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
