# frozen_string_literal: true

require_relative "../../helper"
require "bank/providers/hacienda_cr"

describe Bank::Providers::HaciendaCR do
  let(:ecb_provider) { Object.new }
  let(:provider) { Bank::Providers::HaciendaCR.new(ecb_provider: ecb_provider) }

  it "reports CRC as the supported currency" do
    _(provider.supported_currencies).must_equal(["CRC"])
  end

  it "parses the latest EUR quote into CRC rates" do
    stub_request(:get, "https://api.hacienda.go.cr/indicadores/tc/euro")
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          {
            "fecha" => "2026-03-13",
            "dolares" => 1.1485,
            "colones" => 543.1,
          },
        ),
      )

    _(provider.current).must_equal(
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "CRC" => 543.1 },
        },
      ],
    )
  end

  it "derives historical CRC rates from Hacienda USD quotes and ECB EUR/USD quotes" do
    ecb_provider.define_singleton_method(:historical) do
      [
        {
          date: Date.new(2026, 3, 12),
          rates: { "USD" => 1.0915 },
        },
        {
          date: Date.new(2026, 3, 13),
          rates: { "USD" => 1.0932 },
        },
      ]
    end

    stub_request(:get, "https://api.hacienda.go.cr/indicadores/tc/dolar/historico")
      .with(query: { "d" => "1999-01-04", "h" => Date.today.to_s })
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          [
            {
              "fecha" => "2026-03-12 00:00:00",
              "compra" => 496.4,
              "venta" => 503.71,
            },
            {
              "fecha" => "2026-03-13 00:00:00",
              "compra" => 496.52,
              "venta" => 502.55,
            },
          ],
        ),
      )

    stub_request(:get, "https://api.hacienda.go.cr/indicadores/tc/euro")
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          {
            "fecha" => "2026-03-13",
            "dolares" => 1.1485,
            "colones" => 543.1,
          },
        ),
      )

    _(provider.historical.last(2)).must_equal(
      [
        {
          date: Date.new(2026, 3, 12),
          rates: { "CRC" => 503.71 * 1.0915 },
        },
        {
          date: Date.new(2026, 3, 13),
          rates: { "CRC" => 502.55 * 1.0932 },
        },
      ],
    )
  end

  it "ignores object-shaped historical responses from Hacienda" do
    ecb_provider.define_singleton_method(:historical) do
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "USD" => 1.0932 },
        },
      ]
    end

    stub_request(:get, "https://api.hacienda.go.cr/indicadores/tc/dolar/historico")
      .with(query: { "d" => "1999-01-04", "h" => Date.today.to_s })
      .to_return(
        status: 429,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          {
            "code" => 429,
            "status" => "Too Many Requests",
          },
        ),
      )

    stub_request(:get, "https://api.hacienda.go.cr/indicadores/tc/euro")
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          {
            "fecha" => "2026-03-13",
            "dolares" => 1.1485,
            "colones" => 543.1,
          },
        ),
      )

    _(provider.historical).must_equal(
      [
        {
          date: Date.new(2026, 3, 13),
          rates: { "CRC" => 543.1 },
        },
      ],
    )
  end

  it "appends the current CRC quote when historical data lags behind" do
    ecb_provider.define_singleton_method(:ninety_days) do
      [
        {
          date: Date.new(2026, 2, 13),
          rates: { "USD" => 1.0915 },
        },
      ]
    end

    stub_request(:get, "https://api.hacienda.go.cr/indicadores/tc/dolar/historico")
      .with(query: { "d" => (Date.today - 120).to_s, "h" => Date.today.to_s })
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          [
            {
              "fecha" => "2026-02-13 00:00:00",
              "compra" => 496.52,
              "venta" => 502.55,
            },
          ],
        ),
      )

    stub_request(:get, "https://api.hacienda.go.cr/indicadores/tc/euro")
      .to_return(
        status: 200,
        headers: { "Content-Type" => "application/json" },
        body: Oj.dump(
          {
            "fecha" => "2026-03-13",
            "dolares" => 1.1485,
            "colones" => 543.1,
          },
        ),
      )

    _(provider.ninety_days.last).must_equal(
      {
        date: Date.new(2026, 3, 13),
        rates: { "CRC" => 543.1 },
      },
    )
  end
end
