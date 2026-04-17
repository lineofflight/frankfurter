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
    # The SDMX-CSV response is semicolon-delimited. Rates are published per unit batch
    # (e.g. "100 ATS = x DEM", "1 000 ITL = x DEM", "1 USD = x DEM") — the multiplier is
    # only embedded in the free-text BBK_TITLE column (BBK_UNIT_MULT is always 0 for this
    # dataflow). We hardcode a per-currency multiplier table (MULTIPLIERS) as the source
    # of truth and keep the regex parse (TITLE_MULTIPLIER) as a runtime guard that raises
    # if the published title ever contradicts the table.
    #
    # Records are returned in BBK's native direction — foreign currency as base, DEM as
    # quote — matching the convention used by other pivot-in-quote adapters (e.g. NBG).
    #
    # Attribution required: "Quelle: Deutsche Bundesbank" / "Source: Deutsche Bundesbank".
    # Terms: https://www.bundesbank.de/de/startseite/benutzerhinweise/nutzungsbedingungen-fuer-den-allgemeinen-gebrauch-der-website-763554
    class BBK < Adapter
      SDMX_URL = "https://api.statistiken.bundesbank.de/rest/data/BBEX3/D..DEM.AA.AC.000"
      TITLE_MULTIPLIER = %r{/\s*([\d\s]+?)\s+[A-Z]{3}\s*=}

      class << self
        def backfill_range = 1826 # ~5 years per chunk to avoid ~200 MB single-fetch responses
      end

      # Per-currency multiplier batch sizes from the BBK_TITLE field of each series in
      # the BBEX3 D..DEM.AA.AC.000 dataflow (e.g. "100 ATS = x DEM", "1000 ITL = x DEM").
      # Values are invariant across the 1948-1998 AA series — verified against the
      # recorded VCR cassette and validated at runtime by parse_title_multiplier.
      MULTIPLIERS = {
        "ATS" => 100,
        "BEF" => 100,
        "CAD" => 1,
        "CHF" => 100,
        "DKK" => 100,
        "ESP" => 100,
        "FIM" => 100,
        "FRF" => 100,
        "GBP" => 1,
        "IEP" => 1,
        "ITL" => 1000,
        "JPY" => 100,
        "LUF" => 100,
        "NLG" => 100,
        "NOK" => 100,
        "PTE" => 100,
        "SEK" => 100,
        "USD" => 1,
      }.freeze

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

        code = row["BBK_STD_CURRENCY"]
        return unless code&.match?(/\A[A-Z]{3}\z/)

        value = row["OBS_VALUE"]
        return if value.nil? || value.strip.empty? || value.strip == "."

        multiplier = MULTIPLIERS.fetch(code) do
          raise "BBK: unknown multiplier for #{code}"
        end

        title_multiplier = parse_title_multiplier(row["BBK_TITLE"])
        if title_multiplier && title_multiplier != multiplier
          raise "BBK: title multiplier mismatch for #{code} on #{row["TIME_PERIOD"]}: " \
            "table=#{multiplier}, title=#{title_multiplier} (title: #{row["BBK_TITLE"].inspect})"
        end

        rate = Float(value) / multiplier
        date = Date.parse(row["TIME_PERIOD"])

        { date:, base: code, quote: "DEM", rate: }
      end

      def parse_title_multiplier(title)
        return unless title

        match = title.match(TITLE_MULTIPLIER)
        return unless match

        Integer(match[1].gsub(/\s+/, ""))
      end
    end
  end
end
