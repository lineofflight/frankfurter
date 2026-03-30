# frozen_string_literal: true

require "providers/base"

module Providers
  # Synthetic provider for currencies with long-standing fixed-rate pegs.
  # Generates records from hardcoded peg data — no API calls.
  class PEG < Base
    EARLIEST_DATE = Date.new(1999, 1, 4)

    PEGS = [
      { base: "USD", quote: "BMD", rate: 1.0, since: Date.new(1972, 1, 1) },
      { base: "GBP", quote: "FKP", rate: 1.0, since: Date.new(1966, 1, 1) },
      { base: "GBP", quote: "SHP", rate: 1.0, since: Date.new(1976, 1, 1) },
      { base: "INR", quote: "BTN", rate: 1.0, since: Date.new(1974, 1, 1) },
      { base: "USD", quote: "ANG", rate: 1.79, since: Date.new(1971, 1, 1) },
      { base: "GBP", quote: "GGP", rate: 1.0, since: Date.new(1921, 1, 1) },
      { base: "GBP", quote: "IMP", rate: 1.0, since: Date.new(1840, 1, 1) },
      { base: "GBP", quote: "JEP", rate: 1.0, since: Date.new(1834, 1, 1) },
    ].freeze

    class << self
      def key = "PEG"
      def name = "Currency Pegs"
      def earliest_date = EARLIEST_DATE
    end

    def fetch(since: nil, upto: nil)
      start_date = since || EARLIEST_DATE
      end_date = upto || Date.today

      @dataset = []
      (start_date..end_date).each do |date|
        next if date.saturday? || date.sunday?

        PEGS.each do |peg|
          next if date < peg[:since]

          @dataset << { provider: key, date:, base: peg[:base], quote: peg[:quote], rate: peg[:rate] }
        end
      end

      self
    end
  end
end
