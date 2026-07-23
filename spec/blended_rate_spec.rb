# frozen_string_literal: true

require_relative "helper"
require "blended_rate"
require "carry_forward"
require "peg_anchor"

describe BlendedRate do
  describe ".rebuild" do
    it "materializes the pivot-frame blend for every anchor date" do
      BlendedRate.rebuild

      _(BlendedRate.dataset.count).must_be(:>, 0)

      date = Fixtures.latest_date
      window = Rate.dataset.where(date: (date - CarryForward::LOOKBACK_DAYS)..date).naked.all
      contributors = CarryForward.apply(window, date: date)
      oracle = PegAnchor.apply(Blender.new(contributors, base: "USD").blend, base: "USD")
        .find { |r| r[:quote] == "GBP" }

      stored = BlendedRate.first(quote: "GBP", date: date)

      _(stored).wont_be_nil
      _(stored.rate).must_equal(oracle[:rate])
    end

    it "stores rows sparsely, only where a quote has a fresh observation" do
      d1 = Fixtures.business_day(30)
      d2 = Fixtures.business_day(20)
      # The fake provider carries its own EUR to USD bridge so the pivot rebase can use its rows.
      Rate.dataset.multi_insert([
        { provider: "T1", date: d1, base: "EUR", quote: "MXN", rate: 20.0 },
        { provider: "T1", date: d1, base: "EUR", quote: "USD", rate: 1.2 },
        { provider: "T1", date: d2, base: "EUR", quote: "MXN", rate: 21.0 },
        { provider: "T1", date: d2, base: "EUR", quote: "USD", rate: 1.2 },
      ])

      BlendedRate.rebuild

      _(BlendedRate.where(quote: "MXN").select_order_map(:date)).must_equal([d1, d2])
    end
  end

  describe ".ready?" do
    it "is false while empty, false after a partial refresh, true after a rebuild" do
      _(BlendedRate.ready?).must_equal(false)

      BlendedRate.refresh(Fixtures.latest_date..Fixtures.latest_date)

      _(BlendedRate.dataset.count).must_be(:>, 0)
      _(BlendedRate.ready?).must_equal(false)

      BlendedRate.rebuild

      _(BlendedRate.ready?).must_equal(true)
    end
  end

  describe ".refresh" do
    it "recomputes stored blends inside the window and leaves the rest untouched" do
      BlendedRate.rebuild
      date = Fixtures.latest_date
      before_target = BlendedRate.first(quote: "EUR", date: date).rate
      before_outside = BlendedRate.first(quote: "EUR", date: Fixtures.business_day(30)).rate

      # A late arrival shifts the contributor set for EUR at this anchor. Close enough to the
      # consensus that the outlier filter keeps it.
      Rate.dataset.insert(provider: "T1", date: date, base: "EUR", quote: "USD", rate: 1.10)
      BlendedRate.refresh(date..(date + CarryForward::LOOKBACK_DAYS))

      _(BlendedRate.first(quote: "EUR", date: date).rate).wont_equal(before_target)
      _(BlendedRate.first(quote: "EUR", date: Fixtures.business_day(30)).rate).must_equal(before_outside)
    end

    it "drops stored rows whose anchor date no longer has data" do
      BlendedRate.rebuild
      date = Fixtures.latest_date

      Rate.dataset.where(date: date).delete
      BlendedRate.refresh(date..(date + CarryForward::LOOKBACK_DAYS))

      _(BlendedRate.where(date: date).count).must_equal(0)
    end
  end
end
