# frozen_string_literal: true

require "blended_rollup"
require "monthly_rate"

class BlendedMonthlyRate < Sequel::Model(:blended_monthly_rates)
  include BlendedRollup

  class << self
    def source = MonthlyRate
  end
end
