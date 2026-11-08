# frozen_string_literal: true

require "sequel"

# Weekly and monthly rollups average a pair's daily observations per bucket. Rows pool by Dataset#label, so a provider's
# default datasets roll up together, as their bare provider key reads them: a pair that changed table mid-bucket keeps
# the average of all its observations rather than an unweighted mean of two partial ones. The pooled row is stored under
# the dataset of the bucket's latest observation. Any other dataset rolls up on its own.
module RateRollup
  COLUMNS = [:bucket_date, :dataset_key, :base, :quote, :rate].freeze

  class << self
    # Rollup rows for the given rates rows, in COLUMNS order. The rows must hold every dataset of a pool for the buckets
    # they cover, or the pooled average leaves some observations out.
    def source(rates, bucket)
      require "dataset"

      label = Dataset.label_expression
      latest = { partition: [bucket, label, :base, :quote], order: [Sequel.desc(:date), Sequel.desc(:dataset_key)] }
      rates.select(
        bucket.as(:bucket_date), label.as(:label), :base, :quote, :rate,
        Sequel.function(:first_value, :dataset_key).over(**latest).as(:owner),
      ).from_self
        .select(:bucket_date, :owner, :base, :quote, Sequel.function(:avg, :rate))
        .group(:bucket_date, :label, :base, :quote, :owner)
    end
  end
end
