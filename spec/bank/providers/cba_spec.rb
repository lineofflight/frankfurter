# frozen_string_literal: true

require_relative "../../helper"
require "bank/providers/cba"

describe Bank::Providers::CBA do
  before do
    stub_request(:post, "https://api.cba.am/exchangerates.asmx")
      .with(headers: { "SOAPAction" => "\"http://www.cba.am/ExchangeRatesLatest\"" })
      .to_return(
        status: 200,
        headers: { "Content-Type" => "text/xml" },
        body: <<~XML,
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              <ExchangeRatesLatestResponse xmlns="http://www.cba.am/">
                <ExchangeRatesLatestResult>
                  <CurrentDate>2026-03-13T00:00:00</CurrentDate>
                  <Rates>
                    <ExchangeRate>
                      <ISO>USD</ISO>
                      <Amount>1</Amount>
                      <Rate>377.54</Rate>
                    </ExchangeRate>
                    <ExchangeRate>
                      <ISO>EUR</ISO>
                      <Amount>1</Amount>
                      <Rate>432.7</Rate>
                    </ExchangeRate>
                  </Rates>
                </ExchangeRatesLatestResult>
              </ExchangeRatesLatestResponse>
            </soap:Body>
          </soap:Envelope>
        XML
      )

    stub_request(:post, "https://api.cba.am/exchangerates.asmx")
      .with(headers: { "SOAPAction" => "\"http://www.cba.am/ExchangeRatesByDateRangeByISO\"" })
      .to_return(
        status: 200,
        headers: { "Content-Type" => "text/xml" },
        body: <<~XML,
          <?xml version="1.0" encoding="utf-8"?>
          <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
            <soap:Body>
              <ExchangeRatesByDateRangeByISOResponse xmlns="http://www.cba.am/">
                <ExchangeRatesByDateRangeByISOResult>
                  <diffgr:diffgram xmlns:diffgr="urn:schemas-microsoft-com:xml-diffgram-v1">
                    <DocumentElement xmlns="">
                      <ExchangeRatesByRange>
                        <Rate>436.25</Rate>
                        <Amount>1</Amount>
                        <ISO>EUR</ISO>
                        <RateDate>2026-03-12T00:00:00+04:00</RateDate>
                      </ExchangeRatesByRange>
                      <ExchangeRatesByRange>
                        <Rate>432.7</Rate>
                        <Amount>1</Amount>
                        <ISO>EUR</ISO>
                        <RateDate>2026-03-13T00:00:00+04:00</RateDate>
                      </ExchangeRatesByRange>
                    </DocumentElement>
                  </diffgr:diffgram>
                </ExchangeRatesByDateRangeByISOResult>
              </ExchangeRatesByDateRangeByISOResponse>
            </soap:Body>
          </soap:Envelope>
        XML
      )
  end

  it "reports AMD as the supported currency" do
    provider = Bank::Providers::CBA.new

    _(provider.supported_currencies).must_equal(["AMD"])
  end

  it "parses the latest EUR quote into AMD rates" do
    provider = Bank::Providers::CBA.new

    _(provider.current).must_equal(
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "AMD" => 432.7 },
        },
      ],
    )
  end

  it "parses historical EUR quotes into AMD rates" do
    provider = Bank::Providers::CBA.new

    _(provider.historical.first(2)).must_equal(
      [
        {
          date: Date.new(2026, 3, 12),
          rates: { "AMD" => 436.25 },
        },
        {
          date: Date.new(2026, 3, 13),
          rates: { "AMD" => 432.7 },
        },
      ],
    )
  end
end
