# frozen_string_literal: true

require "db"

class Rate < Sequel::Model(:rates)
  dataset_module do
    def ecb
      where(provider: "ECB")
    end

    # Per-provider latest with adaptive staleness filtering.
    #
    # Instead of finding one global latest date (which drops slower providers),
    # each provider contributes its own most recent snapshot. A provider is
    # included only if its lag from the global max doesn't exceed its publish
    # frequency (the gap between its two most recent dates, default 1 day).
    #
    # This lets daily providers (ECB, BOC) coexist with weekly ones (FRED)
    # without the weekly provider vanishing between publications.
    #
    # When the dataset is already scoped to a single provider (e.g. v1's
    # `Rate.where(provider: "ECB").latest`), this degrades to the simple
    # "find max date" behavior since there's only one provider to consider.
    def latest(date = Date.today)
      date = Date.today if date > Date.today

      # Build the eligible providers query using the current dataset's scope.
      # The subquery respects any existing WHERE clauses (e.g. provider filter).
      scoped_dates_sql = select(:provider, :date).distinct.where(Sequel[:date] <= date).sql

      eligible = model.db[<<~SQL].all
        WITH scoped AS (
          #{scoped_dates_sql}
        ),
        provider_max AS (
          SELECT provider, MAX(date) AS max_date
          FROM scoped
          GROUP BY provider
        ),
        provider_stats AS (
          SELECT
            pm.provider,
            pm.max_date,
            COALESCE(
              CAST(
                julianday(pm.max_date) - julianday(
                  (SELECT s2.date FROM scoped s2
                   WHERE s2.provider = pm.provider AND s2.date < pm.max_date
                   ORDER BY s2.date DESC LIMIT 1)
                )
              AS INTEGER),
              1
            ) AS frequency
          FROM provider_max pm
        )
        SELECT provider, max_date
        FROM provider_stats
        WHERE julianday((SELECT MAX(max_date) FROM provider_stats)) - julianday(max_date) <= frequency
      SQL

      return where(false) if eligible.empty?

      conditions = eligible.map do |row|
        Sequel.&(Sequel[:provider] => row[:provider], Sequel[:date] => row[:max_date])
      end

      where(conditions.reduce { |a, b| a | b })
    end

    def between(interval)
      return where(false) if interval.begin > Date.today

      nearest = Sequel.function(:coalesce, nearest_date_with_rates(interval.begin), interval.begin)
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

    def nearest_date_with_rates(date)
      select(:date)
        .where(Sequel[:date] <= date)
        .order(Sequel.desc(:date))
        .limit(1)
    end
  end
end
