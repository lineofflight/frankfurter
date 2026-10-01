# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # National Bank of the Republic of Belarus. Publishes daily rates for ~30 currencies against BYN.
    #
    # Rates are keyed by an internal currency ID, and NBRB issues a new ID when a currency's terms change: it renumbered
    # its currencies on 2021-07-09 (USD 145 became 431), and BRL moved from a monthly to a daily ID on 2022-08-01. Each
    # ID answers only for its own validity, so the currency reference, which lists every ID with its dates, scale and
    # periodicity, is what reaches back past the latest renumbering.
    class NBRB < Adapter
      BASE_URL = "https://api.nbrb.by/exrates"
      # The dynamics endpoint silently truncates longer ranges to 365 days.
      CHUNK_DAYS = 365

      # BYN replaced BYR at 10,000:1. IDs that predate it return BYR values before this date.
      REDENOMINATION = Date.new(2016, 7, 1)

      class << self
        def backfill_range = CHUNK_DAYS
      end

      def fetch(after: nil, upto: nil)
        start = [after || REDENOMINATION, REDENOMINATION].max
        stop = upto || Date.today

        daily_currencies.flat_map do |currency|
          chunked_dynamics(currency, [start, currency[:from]].max, [stop, currency[:to]].min)
        end
      end

      private

      def daily_currencies
        data = Oj.load(http.get("#{BASE_URL}/currencies").to_s, mode: :strict)
        data.filter_map do |row|
          next unless row.fetch("Cur_Periodicity").zero?

          {
            id: row.fetch("Cur_ID"),
            iso: row.fetch("Cur_Abbreviation"),
            scale: Integer(row.fetch("Cur_Scale")),
            from: Date.parse(row.fetch("Cur_DateStart")),
            to: Date.parse(row.fetch("Cur_DateEnd")),
          }
        end
      end

      def chunked_dynamics(currency, start_date, end_date)
        records = []
        chunk_start = start_date

        while chunk_start <= end_date
          chunk_end = [chunk_start + CHUNK_DAYS - 1, end_date].min
          records.concat(fetch_dynamics(currency, chunk_start, chunk_end))
          chunk_start = chunk_end + 1
        end

        records
      end

      def fetch_dynamics(currency, start_date, end_date)
        response = http.get("#{BASE_URL}/rates/dynamics/#{currency[:id]}", params: {
          startDate: start_date.to_s,
          endDate: end_date.to_s,
        },).to_s
        data = Oj.load(response, mode: :strict)
        raise "NBRB: expected JSON array from dynamics endpoint, got #{data.class}" unless data.is_a?(Array)

        data.filter_map do |row|
          date = Date.parse(row.fetch("Date"))
          next if date.saturday? || date.sunday?

          rate = Float(row.fetch("Cur_OfficialRate"))
          { date:, base: currency[:iso], quote: "BYN", rate: rate / currency[:scale] }
        end
      end
    end
  end
end
