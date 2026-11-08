# frozen_string_literal: true

require "oj"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Ghana interbank FX rates in GHS, from the wpDataTables table (id 40) behind the historical interbank rates
    # page. It runs from 1996-01-02 with a buying, selling and mid rate per currency per business day. The dollar rate
    # is the weighted median of the day's interbank spot deals of US$10,000 or more before 15:30; the rest are crosses.
    # The endpoint filters on the date column server-side and answers an empty 200 without the nonce the page embeds.
    #
    # Each row names its pair: USDGHS prices a dollar in cedis, GHSXOF a cedi in CFA francs. Both are stored as
    # published.
    class BOG < Adapter
      PAGE_URL = "https://www.bog.gov.gh/treasury-and-the-markets/historical-interbank-fx-rates/"
      DATA_URL = "https://www.bog.gov.gh/wp-admin/admin-ajax.php?action=get_wdtable&table_id=40"
      NONCE_PATTERN = /wdtNonceFrontendServerSide_40" value="([^"]+)"/
      DATE_FORMAT = "%d %b %Y"

      # The table labels the old cedi GHS too: 9,264.73 per dollar on 2007-06-27, its last row before the 10,000:1
      # redenomination of 2007-07-01, and 0.9266 on 2007-07-09, its first after.
      PREDECESSORS = { "GHS" => ["GHC", Date.new(2007, 7, 1)] }.freeze

      # BoG kept quoting the old leone after its 2022 redenomination, then moved to new-leone values under the same
      # label: 2,109.3917 per cedi on 2026-09-10, 2.1082 on 2026-09-11.
      SUCCESSORS = { "SLL" => ["SLE", Date.new(2026, 9, 11)] }.freeze

      # "Ecowas" (WAUGHS) is no currency, and no price in cedis either: it falls as the cedi weakens, from 0.3836 in
      # January 2008 to 0.0079 in July 2024 while the dollar went from 0.97 to 14.73 cedis.
      NON_CURRENCY = "WAU"

      class << self
        def backfill_range = 365
      end

      # A run of back-to-back windows draws an occasional 502, so each one pauses before its data request.
      def fetch(after: nil, upto: nil)
        form = {
          "draw" => "1",
          "start" => "0",
          "length" => "-1",
          "columns[0][searchable]" => "true",
          "columns[0][search][value]" => "#{after&.strftime(DATE_FORMAT)}|#{upto&.strftime(DATE_FORMAT)}",
          "wdtNonce" => nonce,
        }
        sleep(2)

        parse(http.post(DATA_URL, form:).to_s)
      end

      def parse(json)
        data = json.is_a?(String) ? Oj.load(json, mode: :strict) : json
        rows = data["data"] if data.is_a?(Hash)
        raise "BOG: data array missing from #{DATA_URL}" unless rows.is_a?(Array)

        rows.filter_map { |row| parse_row(row) }
      end

      private

      def nonce
        http.get(PAGE_URL).to_s[NONCE_PATTERN, 1] || raise("BOG: wdtNonce missing from #{PAGE_URL}")
      end

      def parse_row(row)
        day, _name, pair, bid, ask, mid = row
        return unless pair&.match?(/\A[A-Z]{6}\z/)

        date = Date.strptime(day.strip, DATE_FORMAT)
        base = pair[0, 3]
        quote = pair[3, 3]
        return if base == NON_CURRENCY
        # The cedi-base crosses start in July 2006. Until the redenomination they price the foreign unit in old cedis,
        # the reverse of their label (GHSNGN 71.2 with the naira at 128 to the dollar and the cedi at 9,160), and GHSXOF
        # flits between per-franc and per-1,000 values.
        return if base == "GHS" && date < PREDECESSORS["GHS"].last

        mid = number(mid)
        return unless mid

        # Until 2013 one side is sometimes 0.0000, mostly AUD's bid, and the published mid averages it in. The mid is
        # stored as published and the zero side left out.
        { date:, base: historical_code(base, date), quote: historical_code(quote, date), rate: mid,
          **prices(bid: number(bid), ask: number(ask), mid:), }
      end

      def number(value)
        value = Float(value.to_s.delete(","), exception: false)
        value if value&.positive?
      end
    end
  end
end
