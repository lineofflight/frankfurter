# frozen_string_literal: true

Sequel.migration do
  up do
    create_table(:weekly_rates) do
      column :bucket_date, :date, null: false
      String :provider, null: false
      String :base, null: false
      String :quote, null: false
      Float :rate, null: false

      primary_key [:provider, :bucket_date, :base, :quote]
      index [:bucket_date, :quote]
    end

    create_table(:monthly_rates) do
      column :bucket_date, :date, null: false
      String :provider, null: false
      String :base, null: false
      String :quote, null: false
      Float :rate, null: false

      primary_key [:provider, :bucket_date, :base, :quote]
      index [:bucket_date, :quote]
    end

    # Backfill weekly_rates
    week_num = Sequel.cast(Sequel.function(:strftime, "%W", :date), Integer)
    day_offset = Sequel.join(["+", week_num * 7, " days"])
    year_start = Sequel.function(:strftime, "%Y-01-01", :date)
    week_bucket = Sequel.function(:date, Sequel.function(:strftime, "%Y-%m-%d", year_start, day_offset))

    self[:weekly_rates].insert(
      [:bucket_date, :provider, :base, :quote, :rate],
      self[:rates].select(week_bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
        .group(:provider, :base, :quote, week_bucket),
    )

    # Backfill monthly_rates
    month_bucket = Sequel.function(:strftime, "%Y-%m-01", :date)

    self[:monthly_rates].insert(
      [:bucket_date, :provider, :base, :quote, :rate],
      self[:rates].select(month_bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
        .group(:provider, :base, :quote, month_bucket),
    )
  end

  down do
    drop_table(:weekly_rates)
    drop_table(:monthly_rates)
  end
end
