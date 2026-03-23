# frozen_string_literal: true

require "db"

class Rate < Sequel::Model(:rates)
  dataset_module do
    def ecb
      where(provider: "ECB")
    end

    # Per-provider latest with a 14-day staleness window.
    #
    # Each provider contributes its own most recent snapshot. A provider is included only if its latest date is
    # within 14 days of the global max. This accommodates weekends, holidays, and weekly publishers like FRED
    # (whose worst case is ~13 days: holiday-shortened week plus weekend before next Monday release).
    def latest(date = Date.today)
      date = Date.today if date > Date.today

      scoped_dates_sql = select(:provider, :date).distinct.where(Sequel[:date] <= date).sql

      eligible = model.db[<<~SQL].all
        WITH scoped AS (
          #{scoped_dates_sql}
        ),
        provider_max AS (
          SELECT provider, MAX(date) AS max_date
          FROM scoped
          GROUP BY provider
        )
        SELECT provider, max_date
        FROM provider_max
        WHERE julianday((SELECT MAX(max_date) FROM provider_max)) - julianday(max_date) <= 14
      SQL

      return where(false) if eligible.empty?

      conditions = eligible.map do |row|
        Sequel.&(Sequel[:provider] => row[:provider], Sequel[:date] => row[:max_date])
      end

      where(conditions.reduce { |a, b| a | b })
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
