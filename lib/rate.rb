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
