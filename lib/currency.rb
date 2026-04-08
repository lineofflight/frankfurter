# frozen_string_literal: true

require "currency_coverage"
require "money/currency"
require "peg"

# Model backed by the materialized currencies table. Populated incrementally
# during Provider#backfill. Derives one row per currency with date range.
class Currency < Sequel::Model(:currencies)
  unrestrict_primary_key

  one_to_many :currency_coverages, key: :iso_code
  many_to_many :providers, join_table: :currency_coverages, left_key: :iso_code, right_key: :provider_key

  dataset_module do
    def active
      cutoff = (Date.today - 30).to_s
      where { end_date >= cutoff }
    end

    def with_providers(keys)
      iso_codes = CurrencyCoverage.where(provider_key: keys)
        .select(:iso_code).distinct
      where(iso_code: iso_codes)
    end
  end

  class << self
    def all
      merge_pegged(super)
    end

    def active
      merge_pegged(super.all)
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

  def metadata
    @metadata ||= Money::Currency.find(iso_code)
  end

  def to_h
    {
      iso_code: iso_code,
      iso_numeric: metadata&.iso_numeric,
      name: metadata&.name || iso_code,
      symbol: metadata&.symbol,
      start_date: start_date.to_s,
      end_date: end_date.to_s,
    }
  end

  def providers
    CurrencyCoverage.where(iso_code: iso_code)
      .order(:provider_key)
      .select_map(:provider_key)
  end

  def to_h_with_providers
    if peg
      to_h.merge(peg: { base: peg.base, rate: peg.rate, authority: peg.authority })
    else
      to_h.merge(providers: providers)
    end
  end
end
