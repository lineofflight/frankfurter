# frozen_string_literal: true

require_relative "../helper"
require "quote/base"

module Quote
  describe Base do
    let(:klass) do
      Class.new(Base)
    end

    let(:quote) do
      klass.new(date: Date.today)
    end

    it "requires data" do
      _ { quote.perform }.must_raise(NotImplementedError)
    end

    it "does not know how to format result" do
      _ { quote.formatted }.must_raise(NotImplementedError)
    end

    it "does not know how to generate a cache key" do
      _ { quote.cache_key }.must_raise(NotImplementedError)
    end

    it "defaults base to Euro" do
      _(quote.base).must_equal("EUR")
    end

    it "defaults amount to 1" do
      _(quote.amount).must_equal(1)
    end

    describe "when given data" do
      before do
        def quote.fetch_data
          []
        end
      end

      it "performs" do
        assert quote.perform
      end

      it "performs only once" do
        quote.perform

        refute quote.perform
      end
    end

    describe "when rebasing from an unavailable currency" do
      let(:date) do
        Date.parse("2000-01-01")
      end

      let(:quote) do
        klass.new(date:, base: "ILS")
      end

      before do
        def quote.fetch_data
          [{ date:, iso_code: "USD", rate: 1 }]
        end
      end

      it "finds nothing" do
        quote.perform

        _(quote.not_found?).must_equal(true)
      end
    end

    describe "when rebasing and converting to an unavailable currency" do
      let(:date) do
        Date.today
      end

      let(:quote) do
        klass.new(date:, base: "USD", symbols: ["FOO"])
      end

      before do
        def quote.fetch_data
          [{ date:, iso_code: "USD", rate: 1 }]
        end
      end

      it "finds nothing" do
        quote.perform

        _(quote.not_found?).must_equal(true)
      end
    end
  end
end
