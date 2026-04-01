# frozen_string_literal: true

require "money/currency"
require "peg"
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

    def with_providers(keys)
      rates = Rate.where(provider: keys)
      codes = rates.select(Sequel[:quote].as(:iso_code))
        .union(rates.select(Sequel[:base].as(:iso_code)))
        .from_self.select(:iso_code).distinct
      where(iso_code: codes)
    end
  end

  class << self
    def all
      merge_pegged(super)
    end

    def active
      cutoff = (Date.today - 30).to_s
      merge_pegged(dataset.active.all).select { |c| c.end_date.to_s >= cutoff }
    end

    def map(&block)
      all.map(&block)
    end

    def find(code)
      code = code.upcase
      peg = Peg.find(code)
      if peg
        anchor = where(iso_code: peg.base).first
        return unless anchor

        new_pegged(peg, anchor)
      else
        where(iso_code: code).first
      end
    end

    private

    def merge_pegged(db_currencies)
      anchors = db_currencies.to_h { |c| [c.iso_code, c] }

      pegged = Peg.all.filter_map do |peg|
        anchor = anchors[peg.base]
        next unless anchor

        new_pegged(peg, anchor)
      end

      (db_currencies + pegged).sort_by(&:iso_code)
    end

    def new_pegged(peg, anchor)
      start = [peg.since, Date.parse(anchor.start_date.to_s)].compact.max
      c = new(iso_code: peg.quote, start_date: start, end_date: anchor.end_date)
      c.instance_variable_set(:@peg, peg)
      c
    end
  end

  attr_reader :peg

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
    if peg
      to_h.merge(peg: { base: peg.base, rate: peg.rate, authority: peg.authority })
    else
      to_h.merge(providers: providers)
    end
  end
end
