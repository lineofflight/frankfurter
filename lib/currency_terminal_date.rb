# frozen_string_literal: true

require "date"
require "json"

# Terminal date for a defunct currency code. Backfill drops records whose `base`
# or `quote` is in this table AND whose `date` is on or after the terminal date.
# Most defunct ISO codes still live in the Money::Currency registry, so the
# existing `Money::Currency.find` filter doesn't block them — a provider that
# keeps publishing stale records past the changeover would mark the currency
# active. This is the safety net.
CurrencyTerminalDate = Data.define(:iso_code, :terminal_date, :successor, :ratio, :source, :note) do
  RATE_TABLES = [:rates, :weekly_rates, :monthly_rates].freeze

  class << self
    def all
      @all ||= begin
        file = File.expand_path("../db/seeds/currency_terminal_dates.json", __dir__)
        JSON.parse(File.read(file)).map do |h|
          new(
            iso_code: h.fetch("iso_code"),
            terminal_date: Date.parse(h.fetch("terminal_date")),
            successor: h["successor"],
            ratio: h["ratio"],
            source: h.fetch("source"),
            note: h["note"],
          )
        end.freeze
      end
    end

    def by_code
      @by_code ||= all.to_h { |e| [e.iso_code, e] }
    end

    def find(iso_code)
      by_code[iso_code]
    end

    def expired?(iso_code, date)
      entry = by_code[iso_code]
      return false unless entry

      date >= entry.terminal_date
    end

    # Deletes existing records past each entry's terminal date from rates,
    # weekly_rates, and monthly_rates, then refreshes currencies and
    # currency_coverages for affected codes. Returns a hash of deletion counts.
    def purge(db)
      require "rate"

      totals = { rates: 0, weekly_rates: 0, monthly_rates: 0 }
      affected = []

      db.transaction do
        all.each do |entry|
          cutoff = entry.terminal_date.to_s

          RATE_TABLES.each do |table|
            date_column = table == :rates ? :date : :bucket_date
            count = db[table].where(date_column => cutoff..).where(
              Sequel.|({ base: entry.iso_code }, { quote: entry.iso_code }),
            ).delete
            totals[table] += count
          end

          affected << entry.iso_code
        end

        refresh_summaries(db, affected) unless affected.empty?
      end

      totals
    end

    private

    def refresh_summaries(db, iso_codes)
      iso_codes.each do |code|
        db[:currency_coverages].where(iso_code: code).delete

        db[:rates]
          .where(Sequel.|({ base: code }, { quote: code }))
          .group(:provider)
          .select(
            :provider,
            Sequel.function(:min, :date).as(:start_date),
            Sequel.function(:max, :date).as(:end_date),
          ).each do |row|
            db[:currency_coverages].insert(
              provider_key: row[:provider],
              iso_code: code,
              start_date: row[:start_date],
              end_date: row[:end_date],
            )
          end

        db[:currencies].where(iso_code: code).delete
        global = db[:currency_coverages]
          .where(iso_code: code)
          .select(
            Sequel.function(:min, :start_date).as(:start_date),
            Sequel.function(:max, :end_date).as(:end_date),
          ).first

        next unless global && global[:start_date]

        db[:currencies].insert(
          iso_code: code,
          start_date: global[:start_date],
          end_date: global[:end_date],
        )
      end
    end
  end
end
