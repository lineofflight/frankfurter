# frozen_string_literal: true

# A provider publishes one or more named rate series, each with its own frequency, schedule and metadata. Every provider
# starts with one default dataset keyed like itself, which carries the series fields providers held until now. Seeding
# replaces both tables on boot.
Sequel.migration do
  up do
    create_table(:datasets) do
      String :key, primary_key: true
      String :provider_key, null: false
      TrueClass :is_default, null: false, default: false
      TrueClass :blends
      String :rate_type
      String :frequency, null: false, default: "daily"
      String :publish_cadence
      String :publish_schedule
      String :pivot_currency
      String :data_url
      Date :coverage_start
    end

    columns = [:rate_type, :frequency, :publish_cadence, :publish_schedule, :pivot_currency, :data_url, :coverage_start]
    from(:datasets).insert(
      [:key, :provider_key, :is_default, *columns],
      from(:providers).select(:key, :key, true, *columns),
    )

    alter_table(:providers) do
      columns.each { |column| drop_column(column) }
    end
  end

  down do
    alter_table(:providers) do
      add_column :data_url, String
      add_column :coverage_start, :date
      add_column :pivot_currency, String
      add_column :rate_type, String
      add_column :publish_schedule, String
      add_column :publish_cadence, String
      add_column :frequency, String, null: false, default: "daily"
    end
    from(:datasets).where(is_default: true).each do |dataset|
      fields = dataset.except(:key, :provider_key, :is_default, :blends)
      from(:providers).where(key: dataset[:provider_key]).update(fields)
    end
    drop_table(:datasets)
  end
end
