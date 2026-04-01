# frozen_string_literal: true

require "db"

class Rate < Sequel::Model(DB[:rates].where(outlier: false))
  dataset_module do
    def ecb
      where(provider: "ECB")
    end

    # Per-currency latest with a 14-day staleness window.
    #
    # Each provider contributes its most recent rate for each currency pair.
    # A rate is included only if its date is within 14 days of the requested
    # date. This accommodates providers that publish different currencies on
    # different schedules (e.g. daily + weekly tables from the same bank).
    def latest(date = Date.today)
      latest_dates = where(date: (date - 14)..date)
        .group(:provider, :base, :quote)
        .select(:provider, :base, :quote, Sequel.function(:max, :date).as(:max_date))

      where(Sequel.lit("(provider, base, quote, date) IN (SELECT provider, base, quote, max_date FROM (?) AS ld)", latest_dates))
    end

    def between(interval)
      return where(false) if interval.begin > Date.today

      nearest = Sequel.function(
        :coalesce,
        select(:date).where(Sequel[:date] <= interval.begin).order(Sequel.desc(:date)).limit(1),
        interval.begin,
      )
      where(Sequel[:date] >= nearest)
        .where(Sequel[:date] <= interval.end)
        .order(:date, :quote)
    end

    def only(*quotes)
      where(quote: quotes)
    end

    def downsample(precision)
      sampler = case precision.to_s
      when "week"
        week_num = Sequel.cast(Sequel.function(:strftime, "%W", :date), Integer)
        day_offset = Sequel.join(["+", week_num * 7, " days"])
        year_start = Sequel.function(:strftime, "%Y-01-01", :date)
        Sequel.function(:date, Sequel.function(:strftime, "%Y-%m-%d", year_start, day_offset))
      when "month"
        Sequel.function(:strftime, "%Y-%m-01", :date)
      end

      select(:base, :provider, :quote)
        .select_append { avg(rate).as(rate) }
        .select_append(sampler.as(:date))
        .group(:base, :provider, :quote, sampler)
        .order(:date)
    end
  end
end
