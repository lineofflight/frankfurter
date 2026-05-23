# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/bsh"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe BSh do
      before do
        VCR.insert_cassette("bsh")
      end

      after do
        VCR.eject_cassette
      end

      let(:adapter) { BSh.new }

      it "fetches rates" do
        dataset = adapter.fetch

        _(dataset).wont_be_empty
      end

      it "emits foreign currency as base and ALL as quote" do
        records = adapter.parse(<<~HTML)
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td><td>+0.26</td></TR>
          </TABLE>
        HTML

        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("ALL")
        _(records.first[:rate]).must_equal(82.24)
        _(records.first[:date]).must_equal(Date.new(2026, 5, 22))
      end

      it "scales JPY from per-100 to per-1-unit" do
        records = adapter.parse(<<~HTML)
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>Japanese Yen (100)</TD><TD>JPY</TD><td>51.68</td></TR>
          </TABLE>
        HTML

        _(records.first[:base]).must_equal("JPY")
        _(records.first[:rate]).must_be_close_to(0.5168, 0.0001)
      end

      it "emits XAU and XAG per troy ounce as published" do
        records = adapter.parse(<<~HTML)
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>Gold(OZ 1)</TD><TD>XAU</TD><td>371473.97</td></TR>
            <TR><TD>Silver(OZ 1)</TD><TD>XAG</TD><td>6223.96</td></TR>
          </TABLE>
        HTML

        gold = records.find { |r| r[:base] == "XAU" }
        silver = records.find { |r| r[:base] == "XAG" }

        _(gold[:rate]).must_equal(371473.97)
        _(silver[:rate]).must_equal(6223.96)
      end

      it "excludes SDR" do
        records = adapter.parse(<<~HTML)
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>Special Drawing Rights</TD><TD>SDR</TD><td>112.40</td></TR>
            <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
          </TABLE>
        HTML

        codes = records.map { |r| r[:base] }

        _(codes).wont_include("SDR")
        _(codes).must_include("USD")
      end

      it "parses multiple tables with distinct dates" do
        records = adapter.parse(<<~HTML)
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
          </TABLE>
          <div>Last update:<b>15.05.2026</b></div>
          <TABLE>
            <TR><TD>Hungarian Forint</TD><TD>HUF</TD><td>26.47</td></TR>
          </TABLE>
        HTML

        dates = records.map { |r| r[:date] }.uniq.sort

        _(dates).must_equal([Date.new(2026, 5, 15), Date.new(2026, 5, 22)])
      end

      it "keeps the mid-rate entry when bid/ask repeats a pair on the same date" do
        records = adapter.parse(<<~HTML)
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
          </TABLE>
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>US Dollar</TD><TD>USD</TD><td>81.80</td><td>+0.27</td><td></td><td>82.64</td></TR>
          </TABLE>
        HTML

        usd = records.select { |r| r[:base] == "USD" }

        _(usd.size).must_equal(1)
        _(usd.first[:rate]).must_equal(82.24)
      end

      it "skips rows with non-ISO codes" do
        records = adapter.parse(<<~HTML)
          <div>Last update:<b>22.05.2026</b></div>
          <TABLE>
            <TR><TD>Header</TD><TD>Code</TD><td>Value</td></TR>
            <TR><TD>US Dollar</TD><TD>USD</TD><td>82.24</td></TR>
          </TABLE>
        HTML

        codes = records.map { |r| r[:base] }

        _(codes).must_equal(["USD"])
      end
    end
  end
end
