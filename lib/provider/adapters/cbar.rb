# frozen_string_literal: true

require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of the Republic of Azerbaijan. Publishes a daily bulletin of official rates for ~40 currencies and
    # four precious metals against AZN, one XML file per calendar date at https://www.cbar.az/currencies/DD.MM.YYYY.xml.
    # The archive starts at the 26.11.1993 file, which carries the bulletin of 1993-11-25; earlier URLs redirect.
    #
    # The file for a date carries the latest bulletin effective on or before it, stamped with its own Date attribute, so
    # weekend and holiday files repeat the previous bulletin. Rows are deduplicated on that attribute, not on the
    # requested date.
    class CBAR < Adapter
      URL = "https://www.cbar.az/currencies/"

      # CBAR labels every row in the archive with the currency's current ISO code, including bulletins from before a
      # redenomination, without restating the values: the 2005-12-30 file quotes 1 USD = 4593 "AZN", old manat. Each
      # entry maps the current code to its predecessor and the first date the successor applies, so a row keeps the code
      # of the currency it actually prices.
      PREDECESSORS = {
        "AZN" => ["AZM", Date.new(2006, 1, 1)],
        "BYN" => ["BYR", Date.new(2016, 7, 1)],
        "RUB" => ["RUR", Date.new(1998, 1, 1)],
        "TMT" => ["TMM", Date.new(2009, 1, 1)],
        "TRY" => ["TRL", Date.new(2005, 1, 1)],
      }.freeze

      # Old Turkish lira rows carry Nominal 1 but price 1000 TRL: the last 2004 bulletin quotes 3.61 and the first 2005
      # bulletin, after the 1,000,000:1 redenomination, 3635.15. NBKR publishes the same series per 1000.
      NOMINAL_OVERRIDES = { "TRL" => 1000 }.freeze

      # Labels CBAR uses that aren't ISO 4217 codes.
      ALIASES = { "SDR" => "XDR" }.freeze

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []
        seen = Set.new

        first = true
        (after..end_date).each do |date|
          sleep(0.2) unless first
          first = false

          records = parse(http.get("#{URL}#{date.strftime("%d.%m.%Y")}.xml").to_s)
          next if records.empty? || !seen.add?(records.first[:date])

          dataset.concat(records)
        end

        dataset
      end

      def parse(xml)
        doc = Ox.load(xml)
        root = doc.is_a?(Ox::Document) ? doc.root : doc
        raise "CBAR: expected ValCurs root" unless root.value == "ValCurs"

        date = Date.strptime(root[:Date], "%d.%m.%Y")
        quote = historical_code("AZN", date)

        root.locate("ValType/Valute").filter_map do |valute|
          code = valute[:Code]
          next unless code&.match?(/\A[A-Z]{3}\z/)

          base = historical_code(ALIASES.fetch(code, code), date)
          # Metals are quoted per troy ounce ("1 t.u."); currencies per 1, 100 or 1000 units.
          nominal = text(valute, "Nominal")[/\d+/].to_i * NOMINAL_OVERRIDES.fetch(base, 1)
          rate = Float(text(valute, "Value"), exception: false)
          next unless rate&.positive? && nominal.positive?

          { date:, base:, quote:, rate: rate / nominal }
        end
      end

      private

      def historical_code(code, date)
        predecessor, cutover = PREDECESSORS[code]
        predecessor && date < cutover ? predecessor : code
      end

      def text(node, name)
        node.locate(name).first&.text.to_s.strip
      end
    end
  end
end
