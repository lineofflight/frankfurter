# frozen_string_literal: true

require "sequel"

# Shared SQL bucket expressions for weekly and monthly aggregation. Used by Rate#downsample, rollup migrations, and
# rollup refresh.
module Bucket
  class << self
    def week(date_column = :date)
      Sequel.function(:date, date_column, "-6 days", "weekday 1")
    end

    def month(date_column = :date)
      Sequel.function(:strftime, "%Y-%m-01", date_column)
    end

    def expression(precision, date_column = :date)
      case precision.to_s
      when "week" then week(date_column)
      when "month" then month(date_column)
      end
    end

    # A date range holding every date of the bucket, so a scan can seek a date index before the exact bucket test. Both
    # weekly and monthly buckets are dated at their start (Monday and the 1st), so observations sit within one period
    # after the bucket date.
    def span(precision, bucket_column, date_column = :date)
      to = precision.to_s == "week" ? "+7 days" : "+1 month"
      Sequel.&(
        Sequel[date_column] >= Sequel.function(:date, bucket_column),
        Sequel[date_column] < Sequel.function(:date, bucket_column, to),
      )
    end
  end
end
