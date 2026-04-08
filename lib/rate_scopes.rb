# frozen_string_literal: true

# Shared dataset scopes for rate tables (rates, weekly_rates, monthly_rates).
# Parameterized by date column name and table name.
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
