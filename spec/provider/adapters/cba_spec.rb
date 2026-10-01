# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cba"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBA do
      before do
        VCR.insert_cassette("cba")
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBA.new }

      it "fetches rates since a date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))

        _(dataset).wont_be_empty
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 3, 1))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 1)
      end
    end

    describe "CBA history" do
      before do
        VCR.insert_cassette("cba_history", match_requests_on: [:method, :uri, :body])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBA.new }

      def rates(dataset, code)
        dataset.select { |r| r[:base] == code }.to_h { |r| [r[:date], r[:rate]] }
      end

      it "requests series CBA no longer quotes, under their current codes" do
        dataset = adapter.fetch(after: Date.new(2001, 12, 28), upto: Date.new(2001, 12, 28))
        bases = dataset.map { |r| r[:base] }

        renamed = ["ARS", "BGN", "BRL", "PLN", "UZS", "XDR"]

        _(bases).must_include("DEM")
        _(bases).must_include("DKK")
        _(bases).must_include("BYR")
        _((bases & renamed).sort).must_equal(renamed)
        _(bases & ["ARP", "BGL", "BRC", "PLZ", "SDR", "USM", "TAD", "TMM", "TMT", "TRL", "ROL"]).must_be_empty
        _(rates(dataset, "PLN")[Date.new(2001, 12, 28)]).must_equal(140.83)
      end

      it "reads the som per 10 before CBA's UZS series takes over" do
        dataset = adapter.fetch(after: Date.new(2006, 12, 30), upto: Date.new(2007, 1, 5))
        uzs = rates(dataset, "UZS")

        _(uzs[Date.new(2006, 12, 30)]).must_be_close_to(0.293, 1e-9)
        _(uzs[Date.new(2007, 1, 5)]).must_be_close_to(0.294, 1e-9)
        _(rates(dataset, "PLN").keys.minmax).must_equal([Date.new(2006, 12, 30), Date.new(2007, 1, 5)])
      end

      it "restores the Tajik ruble before the somoni" do
        dataset = adapter.fetch(after: Date.new(2000, 10, 30), upto: Date.new(2000, 11, 1))
        tjr = rates(dataset, "TJR")

        _(tjr.keys).must_equal([Date.new(2000, 10, 30)])
        _(tjr.values.first).must_be_close_to(0.2671, 1e-9)
        _(rates(dataset, "TJS")).must_equal(Date.new(2000, 11, 1) => 250.74)
      end

      it "reads the tenge per 10 before CBA corrected its amount" do
        dataset = adapter.fetch(after: Date.new(2004, 12, 30), upto: Date.new(2005, 1, 4))
        kzt = rates(dataset, "KZT")

        _(kzt[Date.new(2004, 12, 30)]).must_be_close_to(3.737, 1e-9)
        _(kzt[Date.new(2005, 1, 4)]).must_be_close_to(3.739, 1e-9)
      end

      it "reads the krona per 10 before CBA corrected its amount" do
        dataset = adapter.fetch(after: Date.new(2015, 3, 6), upto: Date.new(2015, 3, 9))
        isk = rates(dataset, "ISK")

        _(isk[Date.new(2015, 3, 6)]).must_be_close_to(3.54, 1e-9)
        _(isk[Date.new(2015, 3, 9)]).must_be_close_to(3.512, 1e-9)
      end

      it "collapses the stray BYN row into the old ruble's BYR quote" do
        dataset = adapter.fetch(after: Date.new(2016, 1, 8), upto: Date.new(2016, 1, 8))

        _(rates(dataset, "BYN")).must_be_empty
        _(dataset.count { |r| r[:base] == "BYR" }).must_equal(1)
        _(rates(dataset, "BYR")[Date.new(2016, 1, 8)]).must_be_close_to(0.026, 1e-9)
      end

      it "reads BYR rows after the switch as one new ruble" do
        dataset = adapter.fetch(after: Date.new(2016, 10, 24), upto: Date.new(2016, 10, 28))
        byn = rates(dataset, "BYN")

        _(rates(dataset, "BYR")).must_be_empty
        _(byn.keys.sort).must_equal((Date.new(2016, 10, 24)..Date.new(2016, 10, 28)).to_a)
        _(byn[Date.new(2016, 10, 25)]).must_equal(249.91)
      end

      it "takes the leu from the day CBA gets it right" do
        dataset = adapter.fetch(after: Date.new(2005, 10, 11), upto: Date.new(2005, 10, 12))

        _(rates(dataset, "RON")).must_equal(Date.new(2005, 10, 12) => 149.75)
      end

      it "takes the manat from the day CBA gets it right" do
        dataset = adapter.fetch(after: Date.new(2010, 4, 1), upto: Date.new(2010, 4, 2))

        _(rates(dataset, "TMT")).must_equal(Date.new(2010, 4, 2) => 141.09)
      end

      it "collapses the SDR and XDR duplicates" do
        dataset = adapter.fetch(after: Date.new(2017, 3, 17), upto: Date.new(2017, 3, 17))

        _(dataset.count { |r| r[:base] == "XDR" }).must_equal(1)
      end
    end
  end
end
