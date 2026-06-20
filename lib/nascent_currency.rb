# frozen_string_literal: true

require "date"
require "json"

# A currency code whose published history reaches back before the currency itself existed, paired with the date it came
# into being. Providers sometimes backfill a successor's series with its predecessor's values under the successor code
# (e.g. the Riksbank labels pre-1999 ECU values as EUR); the RateValidation::InceptionDate rule consults this registry
# to reject any row dated before a currency's inception, keeping predecessor values out of the successor's series. Pure
# data: the mirror image of DefunctCurrency at the opposite end of a currency's life.
NascentCurrency = Data.define(:iso_code, :inception_date, :predecessor, :source, :note) do
  # predecessor and note are optional in the seed.
  def initialize(predecessor: nil, note: nil, **) = super

  class << self
    def all
      @all ||= begin
        file = File.expand_path("../db/seeds/nascent_currencies.json", __dir__)
        JSON.parse(File.read(file), symbolize_names: true).map do |attrs|
          new(**attrs, inception_date: Date.parse(attrs[:inception_date]))
        end.freeze
      end
    end

    def by_code
      @by_code ||= all.to_h { |e| [e.iso_code, e] }
    end

    def find(iso_code)
      by_code[iso_code]
    end

    def premature?(iso_code, date)
      entry = by_code[iso_code]
      return false unless entry

      date < entry.inception_date
    end
  end
end
