# frozen_string_literal: true

require_relative "../../helper"
require "oj"
require "bank/providers/nbrb"

describe Bank::Providers::NBRB do
  before do
    stub_request(:get, "https://api.nbrb.by/exrates/rates/EUR?parammode=2")
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          {
            "Cur_ID" => 451,
            "Date" => "2026-03-13T00:00:00",
            "Cur_Abbreviation" => "EUR",
            "Cur_Scale" => 1,
            "Cur_OfficialRate" => 3.3824,
          },
        ),
      )

    stub_request(:get, "https://api.nbrb.by/exrates/rates/dynamics/451")
      .with(query: hash_including("startDate" => "1999-01-04"))
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          [
            {
              "Date" => "2026-03-12T00:00:00",
              "Cur_OfficialRate" => 3.3920,
            },
            {
              "Date" => "2026-03-13T00:00:00",
              "Cur_OfficialRate" => 3.3824,
            },
            {
              "Date" => "2026-03-14T00:00:00",
              "Cur_OfficialRate" => 3.3824,
            },
          ],
        ),
      )
  end

  it "reports BYN as the supported currency" do
    provider = Bank::Providers::NBRB.new

    _(provider.supported_currencies).must_equal(["BYN"])
  end

  it "parses the latest EUR quote into BYN rates" do
    provider = Bank::Providers::NBRB.new

    _(provider.current).must_equal(
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "BYN" => 3.3824 },
        },
      ],
    )
  end

  it "parses historical EUR quotes into BYN rates and skips weekends" do
    provider = Bank::Providers::NBRB.new

    _(provider.historical.last(2)).must_equal(
      [
        {
          date: Date.new(2026, 3, 12),
          rates: { "BYN" => 3.3920 },
        },
        {
          date: Date.new(2026, 3, 13),
          rates: { "BYN" => 3.3824 },
        },
      ],
    )
  end
end
