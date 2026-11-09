# frozen_string_literal: true

require_relative "helper"
require "dataset"
require "provider"

describe Dataset do
  describe "#frequency" do
    it "defaults to daily, blends, and carries forward two weeks" do
      dataset = Dataset.new { |d| d.key = "EXAMPLE" }

      _(dataset.frequency).must_equal("daily")
      _(dataset.blends?).must_equal(true)
      _(dataset.lookback_days).must_equal(14)
    end

    it "keeps monthly and quarterly values out of the blend and carries them across their period" do
      monthly = Dataset.new { |d| d.frequency = "monthly" }
      quarterly = Dataset.new { |d| d.frequency = "quarterly" }

      _(monthly.blends?).must_equal(false)
      _(monthly.lookback_days).must_equal(45)
      _(quarterly.blends?).must_equal(false)
      _(quarterly.lookback_days).must_equal(120)
    end

    it "lets a seed decide whether the series blends" do
      weekly = Dataset.new { |d| d.frequency = "weekly" }
      blending = Dataset.new do |d|
        d.frequency = "weekly"
        d.blends = true
      end
      daily = Dataset.new { |d| d.blends = false }

      _(weekly.blends?).must_equal(false)
      _(blending.blends?).must_equal(true)
      _(blending.lookback_days).must_equal(14)
      _(daily.blends?).must_equal(false)
    end

    it "widens the carry-forward window to the publish cadence when values arrive in arrears" do
      in_arrears = Dataset.new { |d| d.publish_cadence = "monthly" }
      weekly = Dataset.new { |d| d.publish_cadence = "weekly" }
      unscheduled = Dataset.new { |d| d.publish_cadence = nil }

      _(in_arrears.blends?).must_equal(true)
      _(in_arrears.lookback_days).must_equal(45)
      _(weekly.lookback_days).must_equal(14)
      _(unscheduled.lookback_days).must_equal(14)
    end

    it "lists the keys of datasets that do not blend" do
      Dataset.dataset.insert(key: "TST", provider_key: "TST", frequency: "monthly")
      Dataset.load_cache

      _(Dataset.non_blending_keys).must_include("TST")
    ensure
      Dataset.dataset.where(key: "TST").delete
      Dataset.load_cache
    end
  end

  describe "keys and labels" do
    before do
      Provider.dataset.insert(key: "TST", name: "Test")
      Dataset.dataset.multi_insert([
        { key: "TST.A", provider_key: "TST", is_default: true },
        { key: "TST.B", provider_key: "TST", is_default: true },
        { key: "TST.X", provider_key: "TST", is_default: false },
      ])
      Provider.load_cache
      Dataset.load_cache
    end

    after do
      Dataset.dataset.where(provider_key: "TST").delete
      Provider.dataset.where(key: "TST").delete
      Provider.load_cache
      Dataset.load_cache
    end

    it "resolves a provider key to its default datasets" do
      _(Dataset.resolve(["TST"])).must_equal(["TST.A", "TST.B"])
      _(Dataset.resolve(["ECB"])).must_equal(["ECB"])
    end

    it "keeps dataset keys, dedupes, and passes unknown keys through" do
      _(Dataset.resolve(["TST.X", "TST", "TST.A", "NOPE"])).must_equal(["TST.X", "TST.A", "TST.B", "NOPE"])
    end

    it "labels a default dataset's rows with the provider key and others with the dataset key" do
      _(Dataset.label("TST.A")).must_equal("TST")
      _(Dataset.label("TST.B")).must_equal("TST")
      _(Dataset.label("TST.X")).must_equal("TST.X")
      _(Dataset.label("ECB")).must_equal("ECB")
      _(Dataset.label("NOPE")).must_equal("NOPE")
    end

    it "labels rows in SQL the same way" do
      keys = ["TST.A", "TST.B", "TST.X", "ECB", "NOPE"]
      labels = keys.map { |key| DB.get(Dataset.label_expression(Sequel.lit("?", key))) }

      _(labels).must_equal(["TST", "TST", "TST.X", "ECB", "NOPE"])
    end
  end

  describe "#publishes_missed" do
    def build_dataset(schedule, cadence: "daily")
      Dataset.new do |d|
        d.key = "EXAMPLE"
        d.publish_schedule = schedule
        d.publish_cadence = cadence if schedule
      end
    end

    it "returns nil when publish_schedule is nil" do
      dataset = build_dataset(nil, cadence: nil)

      dataset.stub(:end_date, "2026-04-01") do
        _(dataset.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_be_nil
      end
    end

    it "returns nil when end_date is nil" do
      dataset = build_dataset("*/30 14-16 * * 1-5")

      dataset.stub(:end_date, nil) do
        _(dataset.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_be_nil
      end
    end

    it "raises ArgumentError when publish_cadence is unrecognised" do
      bad = build_dataset("*/30 14-16 * * 1-5", cadence: "biweekly")

      bad.stub(:end_date, "2026-04-01") do
        _ { bad.publishes_missed(reference_date: Date.new(2026, 4, 20)) }.must_raise(ArgumentError)
      end
    end

    describe "with daily cadence, Mon-Fri publishing (ECB-style)" do
      let(:mon_fri) { build_dataset("*/30 14-16 * * 1-5") }

      it "returns 0 when end_date is Friday and reference is the following Monday" do
        # Fri 2026-04-17 to Mon 2026-04-20 exclusive: only Sat 18 and Sun 19 sit between — neither is a publish day.
        mon_fri.stub(:end_date, "2026-04-17") do
          _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(0)
        end
      end

      it "counts weekdays missed between end_date and reference" do
        # Last update Mon 2026-04-13, reference Fri 2026-04-17 exclusive: Tue 14, Wed 15, Thu 16 = 3.
        mon_fri.stub(:end_date, "2026-04-13") do
          _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 17))).must_equal(3)
        end
      end

      it "ignores weekends" do
        # Fri 2026-04-10 exclusive to Mon 2026-04-20 exclusive: Mon-Fri 13,14,15,16,17 = 5.
        mon_fri.stub(:end_date, "2026-04-10") do
          _(mon_fri.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(5)
        end
      end
    end

    describe "with daily cadence, seven-day publishing" do
      let(:daily) { build_dataset("*/30 14-16 * * *") }

      it "counts every day between end_date and reference" do
        # (2026-04-10, 2026-04-20) exclusive = 9 days.
        daily.stub(:end_date, "2026-04-10") do
          _(daily.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(9)
        end
      end
    end

    describe "with daily cadence, Mondays-only cron" do
      let(:mondays) { build_dataset("*/30 21-23 * * 1") }

      it "counts only Mondays between end_date and reference" do
        # Mon 2026-04-06 exclusive to Fri 2026-04-24 exclusive: Mondays are 13, 20 = 2.
        mondays.stub(:end_date, "2026-04-06") do
          _(mondays.publishes_missed(reference_date: Date.new(2026, 4, 24))).must_equal(2)
        end
      end
    end

    describe "with weekly cadence (FRED-style, Mon-Tue publishing covers prior ISO week)" do
      let(:fred) { build_dataset("*/30 21-23 * * 1-2", cadence: "weekly") }

      it "returns 0 when end_date is in the week FRED's last batch covered" do
        # Today 2026-04-21 (Tue). Last Monday fire Apr 20 covers prior ISO week Apr 13-19. end_date = 2026-04-19 → same
        # ISO week bucket as Apr 13 → 0 missed.
        fred.stub(:end_date, "2026-04-19") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 21))).must_equal(0)
        end
      end

      it "returns 0 when end_date's ISO week matches expected coverage bucket" do
        # end_date = Saturday 2026-04-18 (same ISO week as Apr 13-19). Still 0 missed.
        fred.stub(:end_date, "2026-04-18") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 21))).must_equal(0)
        end
      end

      it "returns 1 when end_date is one ISO week behind expected" do
        # end_date in week Apr 6-12; expected = week Apr 13-19 → 1 missed.
        fred.stub(:end_date, "2026-04-12") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 21))).must_equal(1)
        end
      end

      it "returns 0 on Sunday before Monday batch arrives (expected is 2 weeks ago)" do
        # Today Sun 2026-04-19. Last Mon fire Apr 13 covers prior week Apr 6-12. end_date = Apr 12 (covered) → 0.
        fred.stub(:end_date, "2026-04-12") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 19))).must_equal(0)
        end
      end

      it "reports 1 missed on Monday morning when prior week's batch did not arrive" do
        # Today Mon 2026-04-20 (FRED's first publish day of this ISO week). end_date Sun 2026-04-12 = end of ISO week
        # Apr 6-12; expected = ISO week Apr 13-19 → 1 missed.
        fred.stub(:end_date, "2026-04-12") do
          _(fred.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(1)
        end
      end
    end

    describe "with monthly cadence (HKMA-style, day-of-month publishing covers prior month)" do
      let(:hkma) { build_dataset("*/30 1-10 3-12 * *", cadence: "monthly") }

      it "returns 0 when current with March data and today is past April's window" do
        hkma.stub(:end_date, "2026-03-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(0)
        end
      end

      it "returns 0 when current with March data and today is before April's window" do
        hkma.stub(:end_date, "2026-03-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 2))).must_equal(0)
        end
      end

      it "returns 0 when last business day is not last calendar day (March 30 edge)" do
        # Nov 30 2025 was a Sunday; last HKMA date was Nov 29. Should still count as Nov covered.
        hkma.stub(:end_date, "2025-11-29") do
          _(hkma.publishes_missed(reference_date: Date.new(2025, 12, 15))).must_equal(0)
        end
      end

      it "returns 1 when April batch did not arrive and today is past May's window" do
        hkma.stub(:end_date, "2026-03-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 5, 15))).must_equal(1)
        end
      end

      it "returns 2 when two monthly batches are missed" do
        hkma.stub(:end_date, "2026-01-31") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 20))).must_equal(2)
        end
      end

      it "reports 1 missed on the first publish day of the window when prior month did not arrive" do
        # Today Apr 3 = first day of HKMA's publish window (DOM 3-12). end_date Feb 28; expected = March → 1 missed.
        hkma.stub(:end_date, "2026-02-28") do
          _(hkma.publishes_missed(reference_date: Date.new(2026, 4, 3))).must_equal(1)
        end
      end
    end

    describe "with quarterly cadence (Treasury-style, fires early in the quarter)" do
      let(:ust) { build_dataset("0 12 1-10 1,4,7,10 *", cadence: "quarterly") }

      it "returns 0 when the latest quarter-end is the last one due" do
        ust.stub(:end_date, "2026-03-31") do
          _(ust.publishes_missed(reference_date: Date.new(2026, 5, 20))).must_equal(0)
        end
      end

      it "counts each quarter missed once its window has started" do
        ust.stub(:end_date, "2025-09-30") do
          _(ust.publishes_missed(reference_date: Date.new(2026, 5, 20))).must_equal(2)
        end
      end
    end

    describe "with monthly cadence on a daily weekday schedule (CBC-style)" do
      # CBC polls a multi-currency open-data file refreshed in monthly batches in arrears: April's daily rows all land
      # in early May. The schedule stays daily so the scheduler keeps polling and catches each batch promptly, but the
      # cadence is monthly so the pre-batch lag is not counted as missed.
      it "reports no missed publishes once the cadence is monthly" do
        cbc = build_dataset("*/30 8-10 * * 1-5", cadence: "monthly")

        # end_date Apr 30, today late May: April is the latest expected bucket until the May batch is due, so nothing is
        # missed.
        cbc.stub(:end_date, "2026-04-30") do
          _(cbc.publishes_missed(reference_date: Date.new(2026, 5, 29))).must_equal(0)
        end
      end

      it "would report a large count under the old daily cadence" do
        cbc = build_dataset("*/30 8-10 * * 1-5", cadence: "daily")

        # Same end_date and reference, but daily cadence counts every weekday since the last data date — the false
        # pre-batch peak.
        cbc.stub(:end_date, "2026-04-30") do
          _(cbc.publishes_missed(reference_date: Date.new(2026, 5, 29))).must_be(:>=, 20)
        end
      end
    end
  end
end
