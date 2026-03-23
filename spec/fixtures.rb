# frozen_string_literal: true

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
  }.freeze

  # Number of business days to generate (~2 years covers downsampling and range tests)
  BUSINESS_DAYS = 520

  class << self
    def seed!
      Rate.dataset.delete
      generate_rates.each_slice(1000) do |batch|
        Rate.dataset.multi_insert(batch)
      end
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

    def generate_rates
      days = business_days
      records = []

      BASE_RATES.each do |provider, config|
        days.each do |date|
          config[:quotes].each do |quote, rate|
            jitter = 1.0 + (date.jd % 100 - 50) * 0.001 # deterministic jitter from date
            records << { provider:, date:, base: config[:base], quote:, rate: (rate * jitter).round(4) }
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
