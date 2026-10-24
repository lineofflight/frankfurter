# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Swiss Federal Tax Administration (ESTV). Its ICTax rate list values five crypto assets (BTC, BCH, ETH, LTC, XRP)
    # and seven major currencies in CHF every Swiss business day, the figures Swiss taxpayers declare at. The crypto
    # values track the market at about 17:00 Zurich time; ESTV publishes no methodology for them.
    #
    # The page's widgets read an undocumented JSON API. A session request sets cookies and returns a CSRF token, then
    # each day takes one POST for tokens and one for currencies. Weekends and holidays come back with null values.
    #
    # The seed keeps the provider out of the blend: crypto is served only when requested by name (#770).
    #
    # Terms: https://www.admin.ch/gov/en/start/terms-and-conditions.html
    class ESTV < Adapter
      API_URL = "https://www.ictax.admin.ch/extern/api"
      COVERAGE_START = Date.new(2018, 1, 2)

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        start_date = [after ? after + 1 : COVERAGE_START, COVERAGE_START].max
        end_date = [upto || Date.today, Date.today].min
        days = (start_date..end_date).reject { |date| date.saturday? || date.sunday? }

        days.each_with_index.flat_map do |date, index|
          sleep(0.3) unless index.zero?
          fetch_date(date)
        end
      end

      # Token and currency responses share a shape: a reference date in epoch milliseconds and entries carrying a code,
      # a CHF value and the number of units it is for (100 for HKD and JPY).
      def parse(json)
        body = Oj.load(json, mode: :strict)
        raise "ESTV: #{body["error"].inspect}" unless body["status"] == "SUCCESS"

        data = body.fetch("data")
        date = Time.at(data.fetch("referenceDate") / 1000, in: "UTC").to_date
        entries = data["tokens"] || data.fetch("currencies")

        entries.filter_map do |entry|
          value = entry["value"]
          units = entry["denomination"].to_i
          next if value.nil? || units.zero?

          code = (entry["token"] || entry["currency"]).fetch("shortName")
          { date:, base: code, quote: "CHF", rate: Float(value) / units }
        end
      end

      private

      def fetch_date(date)
        reference_date = Time.utc(date.year, date.month, date.day).to_i * 1000

        parse(post("tokenGadget", referenceDate: reference_date, tokens: [])) +
          parse(post("exchangeRates", referenceDate: reference_date, currencies: []))
      end

      def post(gadget, body)
        http.headers("Accept" => "application/json", "Cookie" => session[:cookies], "X-CSRF-TOKEN" => session[:token])
          .post("#{API_URL}/coreGadget/#{gadget}.json", json: body)
          .to_s
      end

      def session
        @session ||= begin
          response = http.get("#{API_URL}/authentication/session.json")
          {
            cookies: response.headers.get("Set-Cookie").map { |cookie| cookie.split(";").first }.join("; "),
            token: Oj.load(response.to_s, mode: :strict).dig("data", "csrfToken"),
          }
        end
      end
    end
  end
end
