# frozen_string_literal: true

require "bucket"
require "defunct_currency"
require "money/currency"

# Shared dataset scopes for rate tables (rates, weekly_rates, monthly_rates). Parameterized by date column name and
# table name.
module RateScopes
  class << self
    def included(mod)
      mod.dataset_module(ScopeMethods)
    end

    def named_currencies(dataset)
      codes = Money::Currency.table.keys.map { |code| code.to_s.upcase }
      dataset.where(base: codes, quote: codes)
    end

    def current_currencies(dataset, date_column = :date, precision: nil)
      dataset.exclude(expired_currency_condition(date_column, precision:))
    end

    def expired_currency_condition(date_column = :date, precision: nil)
      conditions = DefunctCurrency.all.map do |entry|
        expired = if precision
                    Sequel[date_column] > Bucket.expression(precision, (entry.terminal_date - 1).to_s)
                  else
                    Sequel[date_column] >= entry.terminal_date.to_s
                  end
        Sequel.&(expired, Sequel.|({ base: entry.iso_code }, { quote: entry.iso_code }))
      end
      conditions.empty? ? false : Sequel.|(*conditions)
    end

    # Recompute a terminal-straddling pair only when expired daily observations contaminate its average. Keep stored
    # precision for every unaffected pair, including boundary buckets whose daily history is incomplete or absent.
    def eligible_rollups(dataset, precision)
      table = dataset.model.table_name
      boundaries = DefunctCurrency.all.map do |entry|
        last_bucket = Bucket.expression(precision, (entry.terminal_date - 1).to_s)
        Sequel.&(
          { bucket_date: last_bucket },
          { last_bucket => Bucket.expression(precision, entry.terminal_date.to_s) },
          Sequel.|({ base: entry.iso_code }, { quote: entry.iso_code }),
        )
      end
      return dataset if boundaries.empty?

      observations = dataset.db[:rates]
        .where([:provider, :base, :quote].to_h { |column| [column, Sequel[table][column]] })
        .where(Bucket.expression(precision) => Sequel[table][:bucket_date])
      expired = expired_currency_condition
      contaminated = Sequel.&(Sequel.|(*boundaries), observations.where(expired).exists)
      eligible = observations.exclude(expired)
      value = Sequel.case({ contaminated => eligible.select(Sequel.function(:avg, :rate)) }, :rate).as(:rate)
      dataset.where(Sequel.|(Sequel.~(contaminated), eligible.exists))
        .select(:bucket_date, :provider, :base, :quote, value).from_self(alias: table)
    end
  end

  module ScopeMethods
    def ecb
      where(provider: "ECB")
    end

    # Provider frequency is guarded because migration 028 rebuilds before 029 adds the column.
    def blendable
      require "provider"
      scope = RateScopes.named_currencies(self)
      if Provider.columns.include?(:frequency)
        keys = Provider.non_blending_keys
        scope = scope.exclude(provider: keys) unless keys.empty?
      end
      precision = { weekly_rates: :week, monthly_rates: :month }[model.table_name]
      scope = RateScopes.current_currencies(scope, model.date_column, precision:)
      precision ? RateScopes.eligible_rollups(scope, precision) : scope
    end

    def between(interval)
      col = model.date_column
      return where(false) if interval.begin > Date.today

      nearest = Sequel.function(
        :coalesce,
        select(col).where(Sequel[col] <= interval.begin).order(Sequel.desc(col)).limit(1),
        interval.begin,
      )
      where(Sequel[col] >= nearest)
        .where(Sequel[col] <= interval.end)
        .order(col, :quote)
    end

    def only(*currencies)
      pivot_currency = Sequel[:providers][:pivot_currency]
      join(:providers, key: :provider)
        .where(Sequel.|({ base: pivot_currency, quote: currencies }, { quote: pivot_currency, base: currencies }))
        .select_all(model.table_name)
    end
  end
end
