# frozen_string_literal: true

require_relative "helper"
require "historical_currency"

describe "Historical currencies" do
  it "registers all historical currencies with the Money gem" do
    seeds = JSON.parse(File.read(File.expand_path("../db/seeds/historical_currencies.json", __dir__)))

    seeds.each do |entry|
      currency = Money::Currency.find(entry["iso_code"])
      _(currency).wont_be_nil "#{entry["iso_code"]} not registered"
      _(currency.name).must_equal(entry["name"])
    end
  end
end
