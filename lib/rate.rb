# frozen_string_literal: true

require "bucket"
require "db"
require "rate_scopes"
require "rate_components"

class Rate < Sequel::Model(:rates)
  include RateScopes

  IDENTITY = [:provider, :date, :base, :quote].freeze

  class << self
    def date_column = :date

    # Enrich existing observations without accepting source revisions or changing an effective rate. A missing row is
    # left to ordinary backfill. Check the database result as well as the adapter oracle before committing the batch.
    def enrich_components(records)
      counts = { updated: 0, missing: 0, revised: 0 }
      db.transaction(savepoint: true) do
        records.each do |record|
          next unless record[:bid] || record[:ask]

          identity = IDENTITY.to_h { |key| [key, record.fetch(key)] }
          scope = where(identity)
          stored = scope.get(:rate)
          expected = RatePrecision.normalize(record[:rate])
          if stored.nil?
            counts[:missing] += 1
          elsif stored != expected
            counts[:revised] += 1
          else
            attributes = RateComponents.attributes(record).slice(:mid, :bid, :ask)
            scope.update(attributes)
            unless scope.get(:rate) == stored
              raise "Component rate differs for #{identity}"
            end

            counts[:updated] += 1
          end
        end
      end
      counts
    end
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
