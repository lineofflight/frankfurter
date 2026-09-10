# frozen_string_literal: true

# Shared dataset scopes for rate tables (rates, weekly_rates, monthly_rates). Parameterized by date column name and
# table name.
module RateScopes
  class << self
    def included(mod)
      mod.dataset_module(ScopeMethods)
    end
  end

  module ScopeMethods
    def ecb
      where(provider: "ECB")
    end

    # Rows eligible for the blend: every provider whose values stand for a day (#646). Loaded lazily and guarded on the
    # column, because migration 028 recomputes the blend on a fresh database before 029 adds frequency.
    def blendable
      require "provider"
      return self unless Provider.columns.include?(:frequency)

      keys = Provider.non_blending_keys
      keys.empty? ? self : exclude(provider: keys)
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
