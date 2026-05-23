# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bcp"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BCP do
      before do
        VCR.insert_cassette("bcp", match_requests_on: [:method, :uri])
      end

      after { VCR.eject_cassette }

      let(:adapter) { BCP.new }

      it "fetches rates with date range" do
        dataset = adapter.fetch(after: Date.new(2024, 6, 2), upto: Date.new(2024, 6, 5))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2024, 6, 2), upto: Date.new(2024, 6, 5))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 5)
      end

      it "stores rates with foreign base and PYG quote" do
        dataset = adapter.fetch(after: Date.new(2024, 6, 2), upto: Date.new(2024, 6, 5))

        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["PYG"])
        _(dataset.map { |r| r[:base] }.uniq).must_include("USD")
      end

      it "returns USD/PYG in a plausible range for mid-2024" do
        dataset = adapter.fetch(after: Date.new(2024, 6, 2), upto: Date.new(2024, 6, 5))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2024, 6, 3) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(7530.0, 100.0)
      end

      it "filters by date range" do
        dataset = adapter.fetch(after: Date.new(2024, 6, 2), upto: Date.new(2024, 6, 4))
        dates = dataset.map { |r| r[:date] }.uniq

        dates.each do |date|
          _(date).must_be(:>, Date.new(2024, 6, 2))
          _(date).must_be(:<=, Date.new(2024, 6, 4))
        end
      end

      it "parses pt-BR decimals correctly" do
        html = build_year_html("USD", 2024, [
          { day: 1, month: 1, value: "7.271,63" },
          { day: 2, month: 1, value: "ND" },
          { day: 3, month: 1, value: "7.281,04" },
        ])

        records = adapter.parse(html, year: 2024, currency: "USD")

        _(records.length).must_equal(2)
        first = records.find { |r| r[:date] == Date.new(2024, 1, 1) }

        _(first[:base]).must_equal("USD")
        _(first[:quote]).must_equal("PYG")
        _(first[:rate]).must_be_close_to(7271.63, 0.001)
      end

      it "skips ND cells" do
        html = build_year_html("EUR", 2024, [
          { day: 1, month: 2, value: "ND" },
          { day: 2, month: 2, value: "7.264,14" },
        ])

        records = adapter.parse(html, year: 2024, currency: "EUR")

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2024, 2, 2))
        _(records.first[:rate]).must_be_close_to(7264.14, 0.001)
      end

      it "skips invalid day-month combinations like Feb 30" do
        html = build_year_html("USD", 2024, [
          { day: 30, month: 2, value: "7.500,00" },
          { day: 30, month: 4, value: "7.500,25" },
        ])

        records = adapter.parse(html, year: 2024, currency: "USD")

        _(records.length).must_equal(1)
        _(records.first[:date]).must_equal(Date.new(2024, 4, 30))
      end

      it "excludes SDR/XDR composite from supported currencies" do
        _(BCP::CURRENCIES).wont_include("XDR")
        _(BCP::CURRENCIES).wont_include("SDR")
      end

      # Builds a minimal year-matrix HTML with the cells specified. Cells outside
      # the list default to ND. Mirrors the structure of the real BCP table.
      def build_year_html(currency, year, cells)
        rows = (1..31).map do |day|
          tds = (1..12).map do |month|
            entry = cells.find { |c| c[:day] == day && c[:month] == month }
            value = entry ? entry[:value] : "ND"
            %(<td style="text-align:center;">#{value}</td>)
          end.join
          %(<tr><th>#{day}</th>#{tds}</tr>)
        end.join

        <<~HTML
          <table id="cotizacion-interbancaria">
            <thead><tr><th colspan="5">PLANILLA DE COTIZACIONES DEL A&Ntilde;O #{year} DE MONEDA #{currency}</th></tr></thead>
          </table>
          <table id="cotizacion-interbancaria"><tbody>#{rows}</tbody></table>
        HTML
      end
    end
  end
end
