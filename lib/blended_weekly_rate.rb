# frozen_string_literal: true

require "blended_rollup"
require "weekly_rate"

class BlendedWeeklyRate < Sequel::Model(:blended_weekly_rates)
  include BlendedRollup

  class << self
    def source = WeeklyRate
  end
end
