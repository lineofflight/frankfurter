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
end
