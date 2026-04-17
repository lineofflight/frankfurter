# frozen_string_literal: true

require "csv"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Deutsche Bundesbank — pre-1999 historical Frankfurt fixings (SERIES_TYPE=AA) from
    # the BBEX3 dataflow. Daily DEM-based rates, 1948-06-21 through 1998-12-30.
    #
    # Post-1999 BBK data mirrors ECB and is intentionally excluded by the hardcoded
    # SERIES_TYPE=AA filter in SDMX_URL.
    #
    # Attribution required: "Quelle: Deutsche Bundesbank" / "Source: Deutsche Bundesbank".
    # Terms: https://www.bundesbank.de/de/startseite/benutzerhinweise/nutzungsbedingungen-fuer-den-allgemeinen-gebrauch-der-website-763554
    class BBK < Adapter
      SDMX_URL = "https://api.statistiken.bundesbank.de/rest/data/BBEX3/D..DEM.AA.AC.000"

      def fetch(after: nil, upto: nil)
        url = URI(SDMX_URL)
        params = { format: "csv" }
        params[:startPeriod] = after.to_s if after
        params[:endPeriod] = upto.to_s if upto
        url.query = URI.encode_www_form(params)

        response = Net::HTTP.get_response(url)
        return [] unless response.is_a?(Net::HTTPSuccess)

        parse(response.body)
      end

      def parse(csv)
        CSV.parse(csv, headers: true, liberal_parsing: true).filter_map do |row|
          parse_row(row)
        end
      end

      private

      def parse_row(row)
        return unless row["FREQ"] == "D"
        return unless row["BBK_ERX_PARTNER_CURRENCY"] == "DEM"

        quote = row["BBK_STD_CURRENCY"]
        return unless quote&.match?(/\A[A-Z]{3}\z/)

        value = row["OBS_VALUE"]
        return if value.nil? || value.strip.empty?

        rate = Float(value)
        date = Date.parse(row["TIME_PERIOD"])

        { date:, base: "DEM", quote:, rate: }
      end
    end
  end
end
