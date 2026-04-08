# frozen_string_literal: true

require "sequel"

# Shared SQL bucket expressions for weekly and monthly aggregation.
# Used by Rate#downsample, rollup migrations, and rollup refresh.
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
  end
end
