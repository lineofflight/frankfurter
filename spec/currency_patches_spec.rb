# frozen_string_literal: true

require_relative "helper"
require "currency_patches"

describe "Currency patches" do
  it "applies all patches to the Money gem" do
    seeds = JSON.parse(File.read(File.expand_path("../db/seeds/currency_patches.json", __dir__)))

    seeds.each do |entry|
      currency = Money::Currency.find(entry["iso_code"])

      _(currency).wont_be_nil("#{entry["iso_code"]} not registered")
      _(currency.name).must_equal(entry["name"])
    end
  end

  it "names retired currencies after CLDR" do
    _(Money::Currency.find("VEB").name).must_equal("Venezuelan Bolívar (1871–2008)")
    _(Money::Currency.find("VEF").name).must_equal("Venezuelan Bolívar (2008–2018)")
    _(Money::Currency.find("SLL").name).must_equal("Sierra Leonean Leone (1964–2022)")
  end

  it "registers the Ecuadorian sucre" do
    currency = Money::Currency.find("ECS")

    _(currency.name).must_equal("Ecuadorian Sucre")
    _(currency.iso_numeric).must_equal("218")
    _(currency.subunit_to_unit).must_equal(100)
  end

  it "registers the convertible Yugoslavian dinar" do
    currency = Money::Currency.find("YUN")

    _(currency.name).must_equal("Yugoslavian Convertible Dinar (1990–1992)")
    _(currency.iso_numeric).must_equal("890")
  end

  it "registers the Czech-Slovak clearing ECU without an ISO numeric code" do
    currency = Money::Currency.find("XCU")

    _(currency.name).must_equal("Czech-Slovak Clearing ECU")
    _(currency.iso_numeric).must_be_nil
  end

  it "registers the COMESA Dollar without an ISO numeric code" do
    currency = Money::Currency.find("CMD")

    _(currency.name).must_equal("COMESA Dollar")
    _(currency.iso_numeric).must_be_nil
  end
end
