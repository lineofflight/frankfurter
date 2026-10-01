# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/nbu"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe NBU do
      before do
        VCR.insert_cassette("nbu", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { NBU.new }

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

      it "restores the Tajikistani ruble code before the 2000 somoni" do
        json = [
          { "exchangedate" => "01.09.2000", "cc" => "TJS", "units" => 1000, "rate" => 2.7776 },
          { "exchangedate" => "02.12.2002", "cc" => "TJS", "units" => 1, "rate" => 1.805263 },
        ]

        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(["TJR", "TJS"])
        _(records.first[:rate]).must_be_close_to(0.0027776, 1e-9)
      end

      it "relabels successors published under retired codes" do
        json = [
          { "exchangedate" => "31.12.1997", "cc" => "RUR", "units" => 10000, "rate" => 3.19 },
          { "exchangedate" => "05.01.1998", "cc" => "RUR", "units" => 10, "rate" => 3.19 },
          { "exchangedate" => "01.07.1999", "cc" => "BGL", "units" => 1000, "rate" => 2.1073 },
          { "exchangedate" => "02.08.1999", "cc" => "BGL", "units" => 1000, "rate" => 2259.4804 },
        ]

        records = adapter.parse(json)

        _(records.map { |r| [r[:date].to_s, r[:base]] }).must_equal([
          ["1997-12-31", "RUR"],
          ["1998-01-05", "RUB"],
          ["1999-07-01", "BGL"],
          ["1999-08-02", "BGN"],
        ])
        records.zip([0.000319, 0.319, 0.0021073, 2.2594804]).each do |record, rate|
          _(record[:rate]).must_be_close_to(rate, rate * 1e-9)
        end
      end

      it "reads successors quoted per 100 under a stale units field" do
        json = [
          { "exchangedate" => "03.01.2000", "cc" => "BGL", "units" => 1000, "rate" => 271.2867 },
          { "exchangedate" => "05.01.2005", "cc" => "TRL", "units" => 10000, "rate" => 0.0376 },
          { "exchangedate" => "27.06.2005", "cc" => "TRL", "units" => 10000, "rate" => 372.9741 },
          { "exchangedate" => "30.06.2005", "cc" => "ROL", "units" => 10000, "rate" => 1.7401 },
          { "exchangedate" => "01.07.2005", "cc" => "ROL", "units" => 10000, "rate" => 169.0657 },
          { "exchangedate" => "05.01.2006", "cc" => "AZM", "units" => 10000, "rate" => 10.995 },
          { "exchangedate" => "06.01.2006", "cc" => "AZM", "units" => 10000, "rate" => 549.8693 },
          { "exchangedate" => "05.01.2009", "cc" => "TMM", "units" => 10000, "rate" => 5.4035 },
          { "exchangedate" => "06.01.2009", "cc" => "TMM", "units" => 10000, "rate" => 270.1754 },
          { "exchangedate" => "04.04.2014", "cc" => "TRY", "units" => 100, "rate" => 541.6837 },
        ]

        records = adapter.parse(json)

        _(records.map { |r| r[:base] }).must_equal(
          ["BGN", "TRL", "TRY", "ROL", "RON", "AZM", "AZN", "TMM", "TMT", "TRY"],
        )
        expected = [
          2.712867, 0.00000376, 3.729741, 0.00017401, 1.690657, 0.0010995, 5.498693, 0.00054035, 2.701754, 5.416837,
        ]

        records.zip(expected).each do |record, rate|
          _(record[:rate]).must_be_close_to(rate, rate * 1e-9)
        end
      end
    end
  end
end
