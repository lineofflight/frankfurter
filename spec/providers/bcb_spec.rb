# frozen_string_literal: true

require_relative "../helper"
require "providers/bcb"

module Providers
  describe BCB do
    let(:provider) { BCB.new }

    before do
      Rate.dataset.delete
      VCR.insert_cassette("bcb")
    end

    after { VCR.eject_cassette }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates since a date" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 7)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 1), upto: Date.new(2026, 3, 7)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    describe "parse" do
      it "parses a normal response" do
        json = {
          value: [
            {
              cotacaoCompra: 5.1995,
              cotacaoVenda: 5.2001,
              dataHoraCotacao: "2026-03-02 13:09:26.433",
              tipoBoletim: "Fechamento",
            },
          ],
        }.to_json

        rates = provider.parse(json, "USD")

        _(rates.length).must_equal(1)
        _(rates[0][:base]).must_equal("USD")
        _(rates[0][:quote]).must_equal("BRL")
        _(rates[0][:rate]).must_be_close_to(5.2001)
      end

      it "extracts the date from dataHoraCotacao" do
        json = {
          value: [
            {
              cotacaoVenda: 5.2001,
              dataHoraCotacao: "2026-03-04 13:09:26.433",
              tipoBoletim: "Fechamento",
            },
          ],
        }.to_json

        rates = provider.parse(json, "USD")

        _(rates[0][:date]).must_equal(Date.new(2026, 3, 4))
      end

      it "returns empty array for empty response" do
        json = { value: [] }.to_json

        rates = provider.parse(json, "USD")

        _(rates).must_be_empty
      end

      it "skips records with missing cotacaoVenda" do
        json = {
          value: [
            {
              cotacaoCompra: 5.1995,
              cotacaoVenda: nil,
              dataHoraCotacao: "2026-03-02 13:09:26.433",
              tipoBoletim: "Fechamento",
            },
          ],
        }.to_json

        rates = provider.parse(json, "USD")

        _(rates).must_be_empty
      end
    end
  end
end
