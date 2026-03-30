# frozen_string_literal: true

require_relative "../helper"
require "providers/mas"

module Providers
  describe MAS do
    before do
      Rate.dataset.delete
      VCR.insert_cassette("mas", match_requests_on: [:method, :host])
    end

    after { VCR.eject_cassette }

    let(:provider) { MAS.new }

    def count_unique_dates
      Rate.select(:date).distinct.count
    end

    it "fetches rates with date range" do
      provider.fetch(since: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24)).import

      _(count_unique_dates).must_be(:>=, 1)
    end

    it "stores multiple currencies per date" do
      provider.fetch(since: Date.new(2026, 3, 16), upto: Date.new(2026, 3, 24)).import
      date = Rate.first.date

      _(Rate.where(date:).count).must_be(:>, 1)
    end

    it "parses records with correct base and quote" do
      records = provider.parse({
        "result" => {
          "records" => [
            {
              "end_of_day" => "2026-03-20",
              "usd_sgd" => "1.3456",
              "eur_sgd" => "1.4567",
              "gbp_sgd" => "1.7234",
            },
          ],
        },
      })

      _(records.length).must_equal(3)

      usd = records.find { |r| r[:base] == "USD" }

      _(usd[:quote]).must_equal("SGD")
      _(usd[:rate]).must_equal(1.3456)
      _(usd[:date]).must_equal(Date.new(2026, 3, 20))
    end

    it "divides per-100 unit rates" do
      records = provider.parse({
        "result" => {
          "records" => [
            {
              "end_of_day" => "2026-03-20",
              "jpy_sgd_100" => "0.8912",
            },
          ],
        },
      })

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("JPY")
      _(records.first[:rate]).must_be_close_to(0.008912, 0.000001)
    end

    it "skips empty and nil values" do
      records = provider.parse({
        "result" => {
          "records" => [
            {
              "end_of_day" => "2026-03-20",
              "usd_sgd" => "1.3456",
              "eur_sgd" => "",
              "gbp_sgd" => nil,
            },
          ],
        },
      })

      _(records.length).must_equal(1)
      _(records.first[:base]).must_equal("USD")
    end

    it "parses multiple dates" do
      records = provider.parse({
        "result" => {
          "records" => [
            {
              "end_of_day" => "2026-03-20",
              "usd_sgd" => "1.3456",
            },
            {
              "end_of_day" => "2026-03-21",
              "usd_sgd" => "1.3478",
            },
          ],
        },
      })

      _(records.length).must_equal(2)
      dates = records.map { |r| r[:date] }.uniq

      _(dates.length).must_equal(2)
    end
  end
end
