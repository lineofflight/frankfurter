# frozen_string_literal: true

require "bucket"
require "db"
require "rate_scopes"

class Rate < Sequel::Model(:rates)
  include RateScopes

  class << self
    def date_column = :date
  end

  dataset_module do
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

    def downsample(precision)
      sampler = Bucket.expression(precision)

      select(:base, :provider, :quote)
        .select_append { avg(rate).as(rate) }
        .select_append(sampler.as(:date))
        .group(:base, :provider, :quote, sampler)
        .order(:date)
    end
  end
end
