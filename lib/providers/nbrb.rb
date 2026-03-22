# frozen_string_literal: true

require "net/http"
require "oj"

require "providers/base"

module Providers
  # National Bank of the Republic of Belarus. Publishes daily rates for ~30 currencies against BYN.
  # BYN was redenominated on 2016-07-01; earlier data uses different currency IDs.
  class NBRB < Base
    RATES_URL = "https://api.nbrb.by/exrates/rates"
    EARLIEST_DATE = Date.new(2016, 7, 1)
    CHUNK_DAYS = 365

    class << self
      def key = "NBRB"
      def name = "National Bank of the Republic of Belarus"
    end

    def fetch(since: nil)
      start_date = since || EARLIEST_DATE
      start_date = Date.parse(start_date.to_s)
      currencies = current_currencies
      @dataset = currencies.flat_map do |cur_id, iso, scale|
        chunked_dynamics(cur_id, iso, scale, start_date, Date.today)
      end
      self
    end

    private

    def parse_daily(data)
      data.filter_map do |row|
        date = Date.parse(row.fetch("Date"))
        next if date.saturday? || date.sunday?

        iso = row.fetch("Cur_Abbreviation")
        scale = Integer(row.fetch("Cur_Scale"))
        rate = Float(row.fetch("Cur_OfficialRate"))
        next if scale.zero?

        { provider: key, date:, base: iso, quote: "BYN", rate: rate / scale }
      end
    end

    def current_currencies
      data = Oj.load(Net::HTTP.get(URI("#{RATES_URL}?periodicity=0")))
      data.map { |row| [row.fetch("Cur_ID"), row.fetch("Cur_Abbreviation"), Integer(row.fetch("Cur_Scale"))] }
    end

    def chunked_dynamics(cur_id, iso, scale, start_date, end_date)
      records = []
      chunk_start = start_date

      while chunk_start <= end_date
        chunk_end = [chunk_start + CHUNK_DAYS - 1, end_date].min
        records.concat(fetch_dynamics(cur_id, iso, scale, chunk_start, chunk_end))
        chunk_start = chunk_end + 1
      end

      records
    end

    def fetch_dynamics(cur_id, iso, scale, start_date, end_date)
      url = URI("#{RATES_URL}/dynamics/#{cur_id}")
      url.query = URI.encode_www_form(startDate: start_date.to_s, endDate: end_date.to_s)
      data = Oj.load(Net::HTTP.get(url))
      return [] unless data.is_a?(Array)

      data.filter_map do |row|
        date = Date.parse(row.fetch("Date"))
        next if date.saturday? || date.sunday?

        rate = Float(row.fetch("Cur_OfficialRate"))
        { provider: key, date:, base: iso, quote: "BYN", rate: rate / scale }
      end
    end
  end
end
