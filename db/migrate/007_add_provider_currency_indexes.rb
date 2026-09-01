# frozen_string_literal: true

# rubocop:disable-next Sequel/ConcurrentIndex
Sequel.migration do
  change do
    add_index :rates, [:provider, :quote], name: :rates_provider_quote_index
    add_index :rates, [:provider, :base], name: :rates_provider_base_index
  end
end
