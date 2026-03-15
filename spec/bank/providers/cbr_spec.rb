# frozen_string_literal: true

require_relative "../../helper"
require "bank/providers/cbr"

describe Bank::Providers::CBR do
  before do
    stub_request(:get, "https://www.cbr.ru/scripts/XML_dynamic.asp")
      .with(query: hash_including("VAL_NM_RQ" => "R01239"))
      .to_return(
        status: 200,
        headers: { "Content-Type" => "text/xml" },
        body: <<~XML,
          <?xml version="1.0" encoding="windows-1251"?>
          <ValCurs ID="R01239" DateRange1="04.01.1999" DateRange2="15.03.2026" name="Foreign Currency Market Dynamic">
            <Record Date="12.03.2026" Id="R01239">
              <Nominal>1</Nominal>
              <Value>91,9378</Value>
              <VunitRate>91,9378</VunitRate>
            </Record>
            <Record Date="13.03.2026" Id="R01239">
              <Nominal>1</Nominal>
              <Value>91,3893</Value>
              <VunitRate>91,3893</VunitRate>
            </Record>
            <Record Date="14.03.2026" Id="R01239">
              <Nominal>1</Nominal>
              <Value>91,9847</Value>
              <VunitRate>91,9847</VunitRate>
            </Record>
          </ValCurs>
        XML
      )
  end

  it "reports RUB as the supported currency" do
    provider = Bank::Providers::CBR.new

    _(provider.supported_currencies).must_equal(["RUB"])
  end

  it "parses the latest EUR quote into RUB rates" do
    provider = Bank::Providers::CBR.new

    _(provider.current).must_equal(
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "RUB" => 91.3893 },
        },
      ],
    )
  end

  it "parses historical EUR quotes into RUB rates and skips weekends" do
    provider = Bank::Providers::CBR.new

    _(provider.historical.last(2)).must_equal(
      [
        {
          date: Date.new(2026, 3, 12),
          rates: { "RUB" => 91.9378 },
        },
        {
          date: Date.new(2026, 3, 13),
          rates: { "RUB" => 91.3893 },
        },
      ],
    )
  end
end
