# frozen_string_literal: true

require "nokogiri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banque Centrale de Tunisie. Publishes daily reference exchange rates for
    # 20 currencies against the Tunisian dinar (TND) on the interbank market.
    #
    # The endpoint accepts a single date per request and returns an HTML
    # fragment with two tables: the interbank reference rates ("Cours Moyens
    # des Devises Cotees") and a manual-exchange table below. We parse only
    # the first table.
    #
    # Caveats handled here:
    # - The meta tag declares ISO-8859-1 but the HTTP header (and observed
    #   bytes) are UTF-8; we trust whichever decoding yields valid characters.
    # - Decimal separator is the French comma; rates may be quoted per 1, per
    #   10, per 100, or per 1000 units (JPY is per 1000).
    # - When the requested date has no data the page either echoes an
    #   "Exhausted Resultset" notice or silently falls back to a nearby
    #   trading day, so we verify the echoed "Journee du DD/MM/YYYY" matches
    #   and otherwise drop the records.
    # - The POST requires a Referer header.
    #
    # The bonus XML endpoint described in #368 returns dates but no values in
    # current responses, so we use the HTML endpoint for all 20 currencies.
    class BCT < Adapter
      URL = "https://www.bct.gov.tn/bct/siteprod/cours_archiv.jsp"
      REFERER = "https://www.bct.gov.tn/bct/siteprod/cours_archive.jsp"

      DATE_RE = %r{Journée du\s*(\d{2})/(\d{2})/(\d{4})}
      SIGLE_RE = /\A([A-Z]{3})\z/
      NUMBER_RE = /[\d.,]+/

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        first = true
        after.upto(end_date) do |date|
          next if date.saturday? || date.sunday?

          sleep(0.5) unless first
          first = false

          dataset.concat(fetch_date(date))
        end

        dataset
      end

      def parse(html, date:)
        echoed = extract_echoed_date(html)
        return [] unless echoed == date

        doc = Nokogiri::HTML.parse(html)

        # Keep only the first interbank table; the second table is for manual exchange.
        table = doc.at_css("table")
        return [] unless table

        records = []
        table.css("tr").each do |row|
          cells = row.css("td").map { |c| c.text.strip }
          next if cells.length < 4

          _name, sigle, unit_str, value_str = cells
          next unless SIGLE_RE.match?(sigle)

          unit = parse_number(unit_str)
          value = parse_number(value_str)
          next if unit.nil? || unit.zero? || value.nil? || value.zero?

          records << { date:, base: sigle, quote: "TND", rate: value / unit }
        end

        records
      end

      private

      def fetch_date(date)
        form = { input: date.strftime("%Y-%m-%d"), langue: "_AN" }
        headers = { "Referer" => REFERER }

        response = http.post(URL, form:, headers:)
        parse(decode(response), date:)
      end

      # The HTML meta tag declares ISO-8859-1 but the HTTP Content-Type header
      # currently reports UTF-8, and the bytes are valid UTF-8. We trust whichever
      # encoding gives valid characters, falling back to a transcode from
      # ISO-8859-1 only if the body isn't valid UTF-8.
      def decode(response)
        body = response.body.to_s.dup
        body.force_encoding("UTF-8")
        return body if body.valid_encoding?

        body.force_encoding("ISO-8859-1").encode("UTF-8", invalid: :replace, undef: :replace)
      end

      def extract_echoed_date(html)
        match = html.match(DATE_RE)
        return unless match

        day, month, year = match.captures
        Date.new(year.to_i, month.to_i, day.to_i)
      rescue Date::Error
        nil
      end

      def parse_number(str)
        return unless str

        cleaned = str.strip
        match = cleaned.match(NUMBER_RE)
        return unless match

        # French format: dot as thousand separator (rare here), comma as decimal.
        Float(match[0].delete(".").tr(",", "."))
      rescue ArgumentError
        nil
      end
    end
  end
end
