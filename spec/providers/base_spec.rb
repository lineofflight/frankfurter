# frozen_string_literal: true

require_relative "../helper"
require "providers/base"

module Providers
  describe Base do
    around do |test|
      Currency.db.transaction do
        test.call
        raise Sequel::Rollback
      end
    end

    it "requires key" do
      _ { Base.new.key }.must_raise(NotImplementedError)
    end

    it "requires base" do
      _ { Base.new.base }.must_raise(NotImplementedError)
    end

    it "requires current" do
      _ { Base.new.current }.must_raise(NotImplementedError)
    end

    it "requires historical" do
      _ { Base.new.historical }.must_raise(NotImplementedError)
    end

    it "defaults dataset to empty" do
      _(Base.new.dataset).must_equal([])
    end

    it "does nothing when importing empty dataset" do
      _(Base.new.import).must_be_kind_of(Base)
    end

    it "imports dataset" do
      provider = Class.new(Base) do
        def key = "TEST"
        def base = "EUR"
      end.new(dataset: [{ date: Date.today, rates: { "USD" => 1.1 } }])

      provider.import
      record = Currency.where(source: "TEST").first

      _(record.base).must_equal("EUR")
      _(record.quote).must_equal("USD")
      _(record.rate).must_equal(1.1)
      _(record.source).must_equal("TEST")
    end
  end
end
