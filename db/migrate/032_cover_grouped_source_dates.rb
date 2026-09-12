# frozen_string_literal: true

# Coverage and snap-back reads select dates while excluding non-blending providers.
Sequel.migration do
  change do
    add_index(:weekly_rates, [:bucket_date, :provider], concurrently: true)
  end
end
