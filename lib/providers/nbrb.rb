# frozen_string_literal: true

require "net/http"
require "oj"

require "providers/base"

module Providers
  # National Bank of the Republic of Belarus. Publishes daily rates for ~30 currencies against BYN.
  class NBRB < Base
    RATES_URL = "https://api.nbrb.by/exrates/rates"

    def key = "NBRB"
    def name = "National Bank of the Republic of Belarus"
    def base = "BYN"

    def current
      data = Oj.load(Net::HTTP.get(URI("#{RATES_URL}?periodicity=0")))
      @dataset = parse_daily(data)
      self
    end

    def historical(start_date: "2016-07-01", end_date: Date.today)
      start_date = Date.parse(start_date.to_s)
      end_date = Date.parse(end_date.to_s)
      currencies = Oj.load(Net::HTTP.get(URI("#{RATES_URL}?periodicity=0")))
      @dataset = currencies.flat_map do |c|
        fetch_dynamics(c.fetch("Cur_ID"), c.fetch("Cur_Abbreviation"), c.fetch("Cur_Scale"), start_date, end_date)
      end
      self
    end

    private

    def parse_daily(data)
      data.filter_map do |row|
        date = Date.parse(row.fetch("Date"))
        next if date.saturday? || date.sunday?

        quote = row.fetch("Cur_Abbreviation")
        scale = Integer(row.fetch("Cur_Scale"))
        rate = Float(row.fetch("Cur_OfficialRate"))
        next if scale.zero?

        { provider: key, date:, base:, quote:, rate: rate / scale }
      end
    end

    def fetch_dynamics(cur_id, quote, scale, start_date, end_date)
      url = URI("#{RATES_URL}/dynamics/#{cur_id}")
      url.query = URI.encode_www_form(startDate: start_date.to_s, endDate: end_date.to_s)
      data = Oj.load(Net::HTTP.get(url))
      return [] unless data.is_a?(Array)

      data.filter_map do |row|
        date = Date.parse(row.fetch("Date"))
        next if date.saturday? || date.sunday?

        rate = Float(row.fetch("Cur_OfficialRate"))
        { provider: key, date:, base:, quote:, rate: rate / scale }
      end
    end
  end
end
