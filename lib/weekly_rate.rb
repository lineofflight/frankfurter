# frozen_string_literal: true

require "db"
require "rate_scopes"

class WeeklyRate < Sequel::Model(:weekly_rates)
  include RateScopes

  class << self
    def date_column = :bucket_date
  end
end
