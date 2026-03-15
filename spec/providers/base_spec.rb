# frozen_string_literal: true

require_relative "../helper"
require "providers/base"

module Providers
  describe Base do
    let(:provider) { Base.new }

    it "requires key" do
      _ { provider.key }.must_raise(NotImplementedError)
    end

    it "requires base" do
      _ { provider.base }.must_raise(NotImplementedError)
    end

    it "requires current" do
      _ { provider.current }.must_raise(NotImplementedError)
    end

    it "requires historical" do
      _ { provider.historical }.must_raise(NotImplementedError)
    end

    it "defaults dataset to empty" do
      _(provider.dataset).must_equal([])
    end

    it "does nothing when importing empty dataset" do
      _(provider.import).must_be_kind_of(Base)
    end

    describe "with a dataset" do
      let(:provider) do
        klass = Class.new(Base) do
          def key = "TEST"
          def base = "EUR"
        end

        klass.new(dataset: [{ date: Date.today, rates: { "USD" => 1.1 } }])
      end

      it "imports" do
        provider.import
        record = Currency.where(source: "TEST").first

        _(record.base).must_equal("EUR")
        _(record.quote).must_equal("USD")
        _(record.rate).must_equal(1.1)
        _(record.source).must_equal("TEST")
      end

      it "upserts" do
        2.times { provider.import }

        _(Currency.where(source: "TEST").count).must_equal(1)
      end
    end
  end
end
