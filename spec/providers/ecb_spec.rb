# frozen_string_literal: true

require_relative "../helper"
require "providers/ecb"

module Providers
  describe ECB do
    before do
      VCR.insert_cassette("feed")
    end

    after do
      VCR.eject_cassette
    end

    let(:provider) { ECB.new }

    it "has key" do
      _(provider.key).must_equal("ECB")
    end

    it "has base" do
      _(provider.base).must_equal("EUR")
    end

    it "fetches current rates" do
      provider.current

      _(provider.dataset.count).must_equal(1)
    end

    it "fetches historical rates" do
      provider.historical

      _(provider.dataset.count).must_be(:>, 90)
    end

    it "parses dates" do
      provider.current
      day = provider.dataset.first

      _(day[:date]).must_be_kind_of(Date)
    end

    it "parses rates" do
      provider.current
      day = provider.dataset.first
      day[:rates].each do |quote, rate|
        _(quote).must_be_kind_of(String)
        _(rate).must_be_kind_of(Float)
      end
    end

    it "returns self for chaining" do
      _(provider.current).must_be_same_as(provider)
      _(provider.import).must_be_same_as(provider)
    end

    it "parses xml" do
      xml = File.read(File.join(__dir__, "../../lib/bank/eurofxref-hist.xml"))
      data = provider.parse(xml)

      _(data).must_be_kind_of(Array)
      _(data.first[:date]).must_be_kind_of(Date)
      _(data.first[:rates]).must_be_kind_of(Hash)
    end
  end
end
