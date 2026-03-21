# frozen_string_literal: true

require "money/currency"
require "rate"

# Virtual model backed by a query over the rates table. Currencies appear as
# both quote and base, so we UNION and re-group to get one row per currency
# with materialized start_date and end_date for efficient filtering.
class Currency < Sequel::Model(
  Rate.select(Sequel[:quote].as(:iso_code))
    .select_append { min(date).as(start_date) }
    .select_append { max(date).as(end_date) }
    .group(:quote)
    .union(
      Rate.select(Sequel[:base].as(:iso_code))
        .select_append { min(date).as(start_date) }
        .select_append { max(date).as(end_date) }
        .group(:base),
      all: true,
    )
    .from_self
    .select(:iso_code)
    .select_append { min(start_date).as(start_date) }
    .select_append { max(end_date).as(end_date) }
    .group(:iso_code)
    .order(:iso_code),
)
  unrestrict_primary_key

  dataset_module do
    def active
      cutoff = (Date.today - 30).to_s
      where { end_date >= cutoff }
    end
  end

  class << self
    def find(code)
      where(iso_code: code.upcase).first
    end
  end

  def money_currency
    @money_currency ||= Money::Currency.find(iso_code)
  end

  def to_h
    {
      iso_code: iso_code,
      iso_numeric: money_currency&.iso_numeric,
      name: money_currency&.name || iso_code,
      symbol: money_currency&.symbol,
      start_date: start_date.to_s,
      end_date: end_date.to_s,
    }
  end

  def providers
    Rate.where(quote: iso_code).or(base: iso_code)
      .select(:provider).distinct.order(:provider).map(:provider)
  end

  def to_h_with_providers
    to_h.merge(providers: providers)
  end
end
