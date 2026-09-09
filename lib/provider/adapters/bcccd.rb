# frozen_string_literal: true

require "date"
require "nokogiri"
require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Banque Centrale du Congo. Publishes a daily indicative mid ("cours indicatif moyen") for a basket of currencies
    # against the Congolese franc (CDF). The bare acronym "BCC" already names Banco Central de Cuba, so the key smushes
    # in the country code: BCCCD.
    #
    # The site, a Next.js relaunch, exposes the fixing two ways and neither is complete on its own:
    #
    # - A dated page per publication day, /cours-de-change/YYYY-MM-DD, back to 2020-10-12. It renders the mid for
    #   ten majors as <dt>/<dd> pairs. Days without a fixing 404 (weekends never have one); a few early nodes 200
    #   with no rows.
    # - The landing page embeds the explorer's history as JSON in its React Server Components payload: buy, mid and
    #   sell for the whole basket (21 codes today, the African neighbours among them), but only weekly before late
    #   August 2025.
    #
    # A fetch reads both: dated pages for the daily majors, the landing page for everything else. Where the two overlap
    # they agree, and the dated page wins. The history occasionally lists a date twice with different values; the later
    # entry is the one the dated page shows, so later entries win within the history too.
    class BCCCD < Adapter
      URL = "https://www.bcc.cd/marche-des-changes/cours-de-change"
      QUOTE = "CDF"
      MID_LABEL = /\A([A-Z]{3}) \(cours moyen\)\z/
      # Each currency's history is an array of flat objects, so nothing inside it opens a bracket.
      HISTORY = /"history":\{((?:"[A-Z]{3}":\[[^\]]*\],?)+)\}/

      class << self
        def backfill_range = 31
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        start_date = after || end_date
        records = {}

        add = ->(record) { records[[record[:date], record[:base], record[:quote]]] = record }

        parse_history(fetch_history).each do |record|
          add.call(record) if record[:date].between?(start_date, end_date)
        end

        (start_date..end_date).each do |date|
          next if date.saturday? || date.sunday?

          parse_day(fetch_day(date), date).each(&add)
          sleep(0.5)
        end

        records.values.sort_by { |r| [r[:date], r[:base]] }
      end

      def parse_day(html, date)
        doc = Nokogiri::HTML.parse(html)

        doc.css("dt").filter_map do |dt|
          code = dt.text.strip[MID_LABEL, 1]
          next unless code

          dd = dt.next_element
          next unless dd&.name == "dd"

          rate = parse_number(dd.text)
          next unless rate&.positive?

          { date:, base: code, quote: QUOTE, rate: }
        end
      end

      def parse_history(payload)
        matches = payload.scan(HISTORY)
        raise "no rate history on #{URL}" if matches.empty?

        matches.flat_map do |(body)|
          Oj.load("{#{body}}", mode: :strict).flat_map do |code, rows|
            rows.filter_map { |row| history_record(code, row) }
          end
        end
      end

      private

      def history_record(code, row)
        rate = row["average"]&.to_f
        unit = row["unit"] || 1
        return unless rate&.positive? && unit.positive?

        rate /= unit unless unit == 1

        { date: Date.iso8601(row["date"]), base: code, quote: QUOTE, rate: }
      end

      # "2 263,0000" on older pages, "2265.71" on newer ones.
      def parse_number(text)
        cleaned = text.gsub(/[[:space:]]/, "")
        cleaned = cleaned.delete(".").tr(",", ".") if cleaned.include?(",")

        Float(cleaned, exception: false)
      end

      def fetch_day(date)
        http.get("#{URL}/#{date.iso8601}").to_s
      rescue HTTP::StatusError => e
        raise unless e.response.code == 404

        ""
      end

      # The RSC header asks Next.js for the component payload alone, without the HTML shell that splits the same JSON
      # across script chunks.
      def fetch_history
        http.headers("RSC" => "1").get(URL).to_s
      end
    end
  end
end
