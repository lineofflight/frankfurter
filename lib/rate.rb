# frozen_string_literal: true

require "db"

class Rate < Sequel::Model(:rates)
  dataset_module do
    def ecb
      where(provider: "ECB")
    end

    def latest(date = Date.today)
      date = Date.today if date > Date.today
      where(date: nearest_date_with_rates(date))
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
