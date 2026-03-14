# frozen_string_literal: true

require_relative "../../helper"
require "bank/providers/bob"

describe Bank::Providers::BOB do
  let(:csv_body) do
    String.new(
      <<~CSV,
        \uFEFFDate,CHN,EUR,GBP,USD,SDR,YEN,ZAR
        "13 Mar 2026",0.5188,0.0655,0.0565,0.0753,0.0554,12.0000,1.2666
        "12 Mar 2026",0.5228,0.0658,0.0568,0.0760,0.0558,12.0800,1.2581
      CSV
      encoding: Encoding::ASCII_8BIT,
    )
  end

  before do
    stub_request(:get, "https://www.bankofbotswana.bw/export/exchange-rates.csv?page&_format=csv")
      .to_return(
        status: 200,
        headers: { "Content-Type" => "text/csv" },
        body: csv_body,
      )
  end

  it "reports BWP as the supported currency" do
    provider = Bank::Providers::BOB.new

    _(provider.supported_currencies).must_equal(["BWP"])
  end

  it "parses EUR quotes into BWP rates" do
    provider = Bank::Providers::BOB.new

    _(provider.historical).must_equal(
      [
        {
          date: Date.new(2026, 3, 12),
          rates: { "BWP" => 1 / 0.0658 },
        },
        {
          date: Date.new(2026, 3, 13),
          rates: { "BWP" => 1 / 0.0655 },
        },
      ],
    )
  end

  it "returns the latest available row for current rates" do
    provider = Bank::Providers::BOB.new

    _(provider.current).must_equal(
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "BWP" => 1 / 0.0655 },
        },
      ],
    )
  end
end
