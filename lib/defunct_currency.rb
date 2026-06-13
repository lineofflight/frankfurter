# frozen_string_literal: true

require "date"
require "json"

# A currency code that has been retired or redenominated, paired with the statutory date its rates stop being valid.
# Pure data: the registry is a reactive safety net, not an exhaustive list of every defunct currency. A code only needs
# an entry when it still lives in the Money::Currency registry (so the `find` filter waves it through) AND a provider
# keeps publishing it past the changeover. The RateValidation::TerminalDate rule consults this; the universal
# RateValidation::FutureDate rule needs no curated list.
DefunctCurrency = Data.define(:iso_code, :terminal_date, :successor, :ratio, :source, :note) do
  # successor, ratio, and note are optional in the seed.
  def initialize(successor: nil, ratio: nil, note: nil, **) = super

  class << self
    def all
      @all ||= begin
        file = File.expand_path("../db/seeds/defunct_currencies.json", __dir__)
        JSON.parse(File.read(file), symbolize_names: true).map do |attrs|
          new(**attrs, terminal_date: Date.parse(attrs[:terminal_date]))
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
  end
end
