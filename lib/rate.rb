# frozen_string_literal: true

require "db"

class Rate < Sequel::Model(:rates)
  dataset_module do
    def latest(date = Date.today)
      date = Date.today if date > Date.today
      where(date: nearest_date_with_rates(date))
    end

    def between(interval)
      return where(false) if interval.begin > Date.today

      where(Sequel.expr(:date) >= Sequel.function(
        :coalesce,
        nearest_date_with_rates(interval.begin),
        interval.begin,
      ))
        .where(Sequel.expr(:date) <= interval.end)
        .order(Sequel.asc(:date), Sequel.asc(:quote))
    end

    def only(*quotes)
      where(quote: quotes)
    end

    def sample(precision)
      sampler = case precision.to_s
      when "day"
        Sequel.function(:strftime, "%Y-%m-%d", :date)
      when "week"
        Sequel.function(
          :date,
          Sequel.function(
            :strftime,
            "%Y-%m-%d",
            Sequel.function(:strftime, "%Y-01-01", :date),
            Sequel.lit("'+' || (CAST(strftime('%W', date) AS INTEGER) * 7) || ' days'"),
          ),
        )
      when "month"
        Sequel.function(:strftime, "%Y-%m-01", :date)
      when "year"
        Sequel.function(:strftime, "%Y-01-01", :date)
      else
        raise ArgumentError, "Invalid precision: #{precision}. Must be one of: week, month, year, day"
      end

      select(:quote)
        .select_append { avg(rate).as(rate) }
        .select_append(sampler.as(:date))
        .group(:quote, sampler)
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
