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

    it "rebuilds in place so ready? remains true throughout" do
      BlendedRate.rebuild

      _(BlendedRate.ready?).must_equal(true)

      observed_ready_states = []
      original_refresh_chunk = BlendedRate.method(:refresh_chunk)
      BlendedRate.define_singleton_method(:refresh_chunk) do |chunk|
        observed_ready_states << BlendedRate.ready?
        original_refresh_chunk.call(chunk)
      end

      begin
        BlendedRate.rebuild

        _(observed_ready_states).wont_be_empty
        _(observed_ready_states.all?(true)).must_equal(true)
      ensure
        BlendedRate.define_singleton_method(:refresh_chunk, original_refresh_chunk)
      end
    end

    it "prunes rows at exact day boundaries outside the active rate range" do
      BlendedRate.rebuild
      first_date = Date.parse(Rate.blendable.min(:date))
      last_date = Date.parse(Rate.blendable.max(:date))

      stale_prev = first_date - 1
      stale_next = last_date + 1
      BlendedRate.dataset.multi_insert([
        { date: stale_prev, quote: "EUR", rate: 1.0 },
        { date: stale_next, quote: "EUR", rate: 1.0 },
      ])

      BlendedRate.rebuild

      _(BlendedRate.where(date: stale_prev).count).must_equal(0)
      _(BlendedRate.where(date: stale_next).count).must_equal(0)
      _(BlendedRate.where(date: first_date).count).must_be(:>, 0)
      _(BlendedRate.where(date: last_date).count).must_be(:>, 0)
    end

    it "prunes contracted boundary dates when historical edge rates are deleted" do
      BlendedRate.rebuild
      old_first = Rate.blendable.min(:date)
      old_last = Rate.blendable.max(:date)

      Rate.dataset.where(date: [old_first, old_last]).delete
      new_first = Rate.blendable.min(:date)
      new_last = Rate.blendable.max(:date)

      BlendedRate.rebuild

      _(BlendedRate.where(date: old_first).count).must_equal(0)
      _(BlendedRate.where(date: old_last).count).must_equal(0)
      _(BlendedRate.min(:date)).must_equal(new_first)
      _(BlendedRate.max(:date)).must_equal(new_last)
      _(BlendedRate.ready?).must_equal(true)
    end

    it "handles a single-date active range where min_date equals max_date" do
      single_date = Fixtures.latest_date
      Rate.dataset.exclude(date: single_date).delete

      BlendedRate.rebuild

      _(BlendedRate.dataset.count).must_be(:>, 0)
      _(BlendedRate.min(:date)).must_equal(single_date.to_s)
      _(BlendedRate.max(:date)).must_equal(single_date.to_s)
      _(BlendedRate.ready?).must_equal(true)
    end

    it "clears the table if there are no blendable rates" do
      BlendedRate.rebuild
      Rate.dataset.delete

      BlendedRate.rebuild

      _(BlendedRate.dataset.count).must_equal(0)
      _(BlendedRate.ready?).must_equal(false)
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

      # A late arrival shifts the contributor set for EUR at this anchor. Close enough to the consensus that the outlier
      # filter keeps it.
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
