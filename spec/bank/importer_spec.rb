# frozen_string_literal: true

require_relative "../helper"
require "bank/importer"

describe Bank::Importer do
  class StubProvider < Bank::Provider
    def initialize(datasets)
      @datasets = datasets
    end

    def current
      @datasets.fetch(:current)
    end

    def ninety_days
      @datasets.fetch(:ninety_days, [])
    end

    def historical
      @datasets.fetch(:historical, [])
    end

    def saved_data
      @datasets.fetch(:saved_data, [])
    end

    def supported_currencies
      []
    end
  end

  it "keeps the first provider as authoritative for overlapping currencies" do
    primary = StubProvider.new(
      current: [
        { date: Date.new(2025, 1, 1), rates: { "USD" => 1.03, "GBP" => 0.83 } },
      ],
    )
    fallback = StubProvider.new(
      current: [
        { date: Date.new(2025, 1, 1), rates: { "USD" => 9.99, "AED" => 3.78 } },
      ],
    )

    result = Bank::Importer.new(providers: [primary, fallback]).current

    _(result).must_equal(
      [
        {
          date: Date.new(2025, 1, 1),
          rates: { "USD" => 1.03, "GBP" => 0.83, "AED" => 3.78 },
        },
      ],
    )
  end

  it "merges dates across providers" do
    provider_one = StubProvider.new(
      historical: [
        { date: Date.new(2025, 1, 1), rates: { "USD" => 1.03 } },
      ],
    )
    provider_two = StubProvider.new(
      historical: [
        { date: Date.new(2025, 1, 2), rates: { "AED" => 3.78 } },
      ],
    )

    result = Bank::Importer.new(providers: [provider_one, provider_two]).historical

    _(result).must_equal(
      [
        { date: Date.new(2025, 1, 1), rates: { "USD" => 1.03 } },
        { date: Date.new(2025, 1, 2), rates: { "AED" => 3.78 } },
      ],
    )
  end

  it "includes the Costa Rica provider by default" do
    provider_classes = Bank::Importer.providers.map(&:class)

    _(provider_classes).must_include(Bank::Providers::HaciendaCR)
  end
end
