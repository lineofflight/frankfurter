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
      klass = Class.new(Base) do
        def key = "TEST"
        def name = "Test"
        def base = "EUR"
      end

      _(klass.new.import).must_be_kind_of(Base)
    ensure
      Providers.all.delete(klass)
    end

    describe "with a dataset" do
      let(:dataset) do
        [{ date: Date.today, provider: "TEST", base: "EUR", quote: "USD", rate: 1.1 }]
      end

      let(:provider) do
        klass = Class.new(Base) do
          def key = "TEST"
          def name = "Test"
          def base = "EUR"
        end
        klass.new(dataset:)
      end

      after do
        Providers.all.delete(provider.class)
      end

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
    end
  end
end
