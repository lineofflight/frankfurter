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
    # The SDMX-CSV response is semicolon-delimited. Rates are quoted per unit batch
    # (e.g. "100 ATS = x DEM", "1 000 ITL = x DEM", "1 USD = x DEM"); the multiplier is
    # only embedded in the free-text BBK_TITLE column (BBK_UNIT_MULT is always 0 for
    # this dataflow), so we parse it out and normalize to "1 quote = rate DEM".
    #
    # Attribution required: "Quelle: Deutsche Bundesbank" / "Source: Deutsche Bundesbank".
    # Terms: https://www.bundesbank.de/de/startseite/benutzerhinweise/nutzungsbedingungen-fuer-den-allgemeinen-gebrauch-der-website-763554
    class BBK < Adapter
      SDMX_URL = "https://api.statistiken.bundesbank.de/rest/data/BBEX3/D..DEM.AA.AC.000"
      TITLE_MULTIPLIER = %r{/\s*([\d\s]+?)\s+[A-Z]{3}\s*=}

      def fetch(after: nil, upto: nil)
        url = URI(SDMX_URL)
        params = { format: "sdmx_csv" }
        params[:startPeriod] = after.to_s if after
        params[:endPeriod] = upto.to_s if upto
        url.query = URI.encode_www_form(params)

        response = Net::HTTP.get_response(url)
        response.value

        parse(response.body)
      end

      def parse(csv)
        CSV.parse(csv, headers: true, liberal_parsing: true, col_sep: ";").filter_map do |row|
          parse_row(row)
        end
      end

      private

      def parse_row(row)
        return unless row["BBK_STD_FREQ"] == "D"
        return unless row["BBK_ERX_PARTNER_CURRENCY"] == "DEM"

        quote = row["BBK_STD_CURRENCY"]
        return unless quote&.match?(/\A[A-Z]{3}\z/)

        value = row["OBS_VALUE"]
        return if value.nil? || value.strip.empty? || value.strip == "."

        rate = Float(value) / multiplier(row["BBK_TITLE"])
        date = Date.parse(row["TIME_PERIOD"])

        { date:, base: "DEM", quote:, rate: }
      end

      def multiplier(title)
        return 1 unless title

        match = title.match(TITLE_MULTIPLIER)
        return 1 unless match

        Integer(match[1].gsub(/\s+/, ""))
      end
    end
  end
end
