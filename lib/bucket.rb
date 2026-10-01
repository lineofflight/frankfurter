# frozen_string_literal: true

require "sequel"

# Shared SQL bucket expressions for weekly and monthly aggregation. Used by Rate#downsample, rollup migrations, and
# rollup refresh.
module Bucket
  class << self
    def week(date_column = :date)
      week_num = Sequel.cast(Sequel.function(:strftime, "%W", date_column), Integer)
      day_offset = Sequel.join(["+", week_num * 7, " days"])
      year_start = Sequel.function(:strftime, "%Y-01-01", date_column)
      Sequel.function(:date, Sequel.function(:strftime, "%Y-%m-%d", year_start, day_offset))
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

    # A date range holding every date of the bucket, so a scan can seek a date index before the exact bucket test. Week
    # buckets count whole weeks from 1 January, so a week's dates sit from 7 days before its bucket date to 5 after.
    def span(precision, bucket_column, date_column = :date)
      from, to = precision.to_s == "week" ? ["-7 days", "+6 days"] : ["+0 days", "+1 month"]
      Sequel.&(
        Sequel[date_column] >= Sequel.function(:date, bucket_column, from),
        Sequel[date_column] < Sequel.function(:date, bucket_column, to),
      )
    end
  end
end
