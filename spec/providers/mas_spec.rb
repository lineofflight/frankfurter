# frozen_string_literal: true

require_relative "../helper"
require "providers/mas"

module Providers
  describe MAS do
    let(:provider) { MAS.new }

    it "parses JSON response" do
      data = {
        "result" => {
          "records" => [
            {
              "end_of_day" => "2025-03-24",
              "usd_sgd" => "1.3456",
              "eur_sgd" => "1.4567",
              "jpy_sgd_100" => "0.8912",
            },
          ],
        },
      }

      records = provider.parse(data)

      _(records.size).must_equal(3)

      usd = records.find { |r| r[:base] == "USD" }
      _(usd[:quote]).must_equal("SGD")
      _(usd[:rate]).must_be_close_to(1.3456)
      _(usd[:date]).must_equal(Date.new(2025, 3, 24))
      _(usd[:provider]).must_equal("MAS")

      # JPY is per 100 units
      jpy = records.find { |r| r[:base] == "JPY" }
      _(jpy[:rate]).must_be_close_to(0.008912)
    end

    it "skips empty or null values" do
      data = {
        "result" => {
          "records" => [
            {
              "end_of_day" => "2025-03-24",
              "usd_sgd" => "1.3456",
              "eur_sgd" => "",
              "jpy_sgd_100" => nil,
            },
          ],
        },
      }

      records = provider.parse(data)

      _(records.size).must_equal(1)
      _(records.first[:base]).must_equal("USD")
    end

    it "handles multiple dates" do
      data = {
        "result" => {
          "records" => [
            { "end_of_day" => "2025-03-24", "usd_sgd" => "1.3456" },
            { "end_of_day" => "2025-03-25", "usd_sgd" => "1.3500" },
          ],
        },
      }

      records = provider.parse(data)

      _(records.size).must_equal(2)
      dates = records.map { |r| r[:date] }.uniq
      _(dates.size).must_equal(2)
    end
  end
end
