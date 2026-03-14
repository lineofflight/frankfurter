# frozen_string_literal: true

require_relative "../../helper"
require "oj"
require "bank/providers/nbu"

describe Bank::Providers::NBU do
  before do
    stub_request(:get, "https://bank.gov.ua/NBU_Exchange/exchange_site")
      .with(
        query: hash_including(
          "valcode" => "EUR",
          "sort" => "exchangedate",
          "order" => "asc",
          "json" => "",
        ),
      )
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          [
            {
              "exchangedate" => "13.03.2026",
              "cc" => "EUR",
              "rate" => 45.0912,
              "rate_per_unit" => 1,
            },
            {
              "exchangedate" => "16.03.2026",
              "cc" => "EUR",
              "rate" => 45.3201,
              "rate_per_unit" => 1,
            },
          ],
        ),
      )
  end

  it "reports UAH as the supported currency" do
    provider = described_class.new

    _(provider.supported_currencies).must_equal(["UAH"])
  end

  it "parses historical EUR quotes into UAH rates" do
    provider = described_class.new
    result = provider.historical

    _(result).must_equal(
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "UAH" => 45.0912 },
        },
        {
          date: Date.new(2026, 3, 16),
          rates: { "UAH" => 45.3201 },
        },
      ],
    )
  end

  it "returns the last available row for current rates" do
    provider = described_class.new
    days = [
      {
        date: Date.new(2026, 3, 13),
        rates: { "UAH" => 45.0912 },
      },
      {
        date: Date.new(2026, 3, 16),
        rates: { "UAH" => 45.3201 },
      },
    ]

    result = provider.stub(:range, days) { provider.current }

    _(result).must_equal(
      [
        {
          date: Date.new(2026, 3, 16),
          rates: { "UAH" => 45.3201 },
        },
      ],
    )
  end
end
