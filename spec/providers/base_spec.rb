# frozen_string_literal: true

require_relative "../helper"
require "providers/base"

module Providers
  describe Base do
    let(:klass) do
      Class.new(Base) do
        def key = "TEST"
        def name = "Test"
        def base = "EUR"
      end
    end

    let(:provider) { klass.new }

    after do
      Providers.all.delete(klass)
    end

    it "requires current" do
      _ { provider.current }.must_raise(NotImplementedError)
    end

    it "requires historical" do
      _ { provider.historical }.must_raise(NotImplementedError)
    end

    describe "with a dataset" do
      let(:dataset) do
        [{ date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 1.1 }]
      end

      let(:provider) { klass.new(dataset:) }

      it "imports" do
        provider.import
        record = Rate.where(provider: "TEST").first

        _(record.base).must_equal("EUR")
        _(record.quote).must_equal("USD")
        _(record.rate).must_equal(1.1)
        _(record.provider).must_equal("TEST")
      end

      it "upserts" do
        2.times { provider.import }

        _(Rate.where(provider: "TEST").count).must_equal(1)
      end

      it "excludes non-currency quotes" do
        dataset << { date: Date.today, provider: "TEST", base: "EUR", quote: "XAU", rate: 2000.0 }
        provider.import

        _(Rate.where(provider: "TEST", quote: "XAU").count).must_equal(0)
      end
    end
  end
end
