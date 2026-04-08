# frozen_string_literal: true

require "bucket"
require "rate"

# Generates realistic test data for ECB and BOC providers.
# All dates are relative to today so tests never go stale.
module Fixtures
  BASE_RATES = {
    "ECB" => {
      base: "EUR",
      quotes: {
        "USD" => 1.08,
        "GBP" => 0.86,
        "JPY" => 160.0,
        "CAD" => 1.47,
        "INR" => 90.0,
        "CHF" => 0.95,
        "SEK" => 11.2,
        "NOK" => 11.5,
        "PLN" => 4.3,
        "CZK" => 25.1,
      },
    },
    "BOC" => {
      base: "CAD",
      quotes: { "USD" => 0.74, "EUR" => 0.68, "GBP" => 0.58, "JPY" => 109.0 },
    },
    "BOJ" => {
      mixed: [
        { base: "EUR", quote: "USD", rate: 1.08 },
        { base: "USD", quote: "JPY", rate: 155.0 },
      ],
    },
  }.freeze

  # Number of business days to generate (~2 years covers downsampling and range tests)
  BUSINESS_DAYS = 520

  class << self
    def seed!
      Rate.dataset.delete
      generate_rates.each_slice(1000) do |batch|
        Rate.dataset.multi_insert(batch)
      end
      rebuild_rollups!
      rebuild_currencies!
    end

    # The most recent business day in the fixture (useful for tests)
    def latest_date
      business_days.first
    end

    # A business day roughly N calendar days ago (guaranteed to be in the fixture)
    def business_day(days_ago)
      business_days.find { |d| d <= Date.today - days_ago }
    end

    # Find a recent Sunday for weekend snap tests
    def recent_sunday
      date = Date.today
      date -= 1 until date.sunday?
      date
    end

    # Find the preceding Friday for a given weekend date
    def preceding_friday(date)
      date -= 1 until date.friday?
      date
    end

    private

    def rebuild_rollups!
      db = Sequel::Model.db

      db[:weekly_rates].delete
      week_bucket = Bucket.week
      db[:weekly_rates].insert(
        [:bucket_date, :provider, :base, :quote, :rate],
        db[:rates].select(week_bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
          .group(:provider, :base, :quote, week_bucket),
      )

      db[:monthly_rates].delete
      month_bucket = Bucket.month
      db[:monthly_rates].insert(
        [:bucket_date, :provider, :base, :quote, :rate],
        db[:rates].select(month_bucket, :provider, :base, :quote, Sequel.function(:avg, :rate))
          .group(:provider, :base, :quote, month_bucket),
      )
    end

    def rebuild_currencies!
      db = Sequel::Model.db

      db[:currencies].delete
      db.run(<<~SQL)
        INSERT INTO currencies (iso_code, start_date, end_date)
        SELECT iso_code, MIN(start_date), MAX(end_date)
        FROM (
          SELECT quote AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
          FROM rates GROUP BY quote
          UNION ALL
          SELECT base AS iso_code, MIN(date) AS start_date, MAX(date) AS end_date
          FROM rates GROUP BY base
        )
        GROUP BY iso_code
        ORDER BY iso_code
      SQL

      db[:currency_coverages].delete
      db.run(<<~SQL)
        INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
        SELECT provider, iso_code, MIN(date), MAX(date)
        FROM (
          SELECT provider, quote AS iso_code, date FROM rates
          UNION ALL
          SELECT provider, base AS iso_code, date FROM rates
        )
        GROUP BY provider, iso_code
        ORDER BY provider, iso_code
      SQL
    end

    def generate_rates
      days = business_days
      records = []

      BASE_RATES.each do |provider, config|
        days.each do |date|
          jitter = 1.0 + (date.jd % 100 - 50) * 0.001 # deterministic jitter from date
          if config[:mixed]
            config[:mixed].each do |pair|
              records << { provider:, date:, base: pair[:base], quote: pair[:quote], rate: (pair[:rate] * jitter).round(4) }
            end
          else
            config[:quotes].each do |quote, rate|
              records << { provider:, date:, base: config[:base], quote:, rate: (rate * jitter).round(4) }
            end
          end
        end
      end

      records
    end

    def business_days
      @business_days ||= begin
        days = []
        date = Date.today
        while days.size < BUSINESS_DAYS
          days << date unless date.saturday? || date.sunday?
          date -= 1
        end
        days
      end
    end
  end
end
