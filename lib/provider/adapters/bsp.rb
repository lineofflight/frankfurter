# frozen_string_literal: true

require "date"
require "json"
require "pdf-reader"
require "stringio"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bangko Sentral ng Pilipinas, the daily Reference Exchange Rate Bulletin
    # (RERB), published every business day (Mon-Fri, no weekends or Philippine
    # holidays). Each bulletin is a single PDF.
    #
    # We relay one figure from it: the "BSP Reference Rate", BSP's own official
    # USD/PHP mid (1 USD = X PHP). It is the only rate in the bulletin BSP
    # actually computes.
    #
    # The rest of the bulletin is third-party data BSP reprints, which we do not
    # relay: a "PHIL. PESO EQUIVALENT" table of ~32 currencies sourced from LSEG
    # (Refinitiv) closing prices, plus an SDR rate (IMF) and gold/silver buying
    # prices (LBMA). Each is already covered with ample independent depth by
    # official sources elsewhere in the blend (PHP crosses by 20+ central banks,
    # XDR by the IMF and ~24 others, gold and silver by 5 to 10 metal providers),
    # so BSP's reprints would add a correlated, internally inconsistent input
    # rather than an independent one. The peso table is anchored on LSEG's
    # USD/PHP, which differs from the Reference Rate we publish. See #533.
    #
    # The bulletins live in a SharePoint list named "RERB", exposed without
    # authentication via the SharePoint REST API. Each list item carries a Title
    # holding the bulletin date as DDMMMYYYY (e.g. "29May2026") and an attachment,
    # the PDF, reachable at its ServerRelativeUrl. We enumerate the list (newest
    # first), collect items whose date falls in the requested window, fetch each
    # PDF, and read the Reference Rate line with pdf-reader.
    #
    # Coverage starts 2017-11-06: the SharePoint list reaches back to 2017-01-03,
    # but bulletins before 2017-11-06 are image-only scans with no extractable
    # text layer. The adapter fetches them harmlessly (empty text yields no
    # records) but cannot parse them, so coverage_start is pinned to the first
    # text-backed bulletin.
    class BSP < Adapter
      HOST = "https://www.bsp.gov.ph"
      LIST_URL = "#{HOST}/_api/web/lists/getByTitle('RERB')/items".freeze
      PAGE_SIZE = 100

      REFERENCE_RATE_PATTERN = /BSP Reference Rate:\s*PHP\s+([\d.,]+)/

      class << self
        # Each request fetches a single day's PDF, so chunk the archive to keep
        # progress durable. fetch_each upserts after every window.
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        upto ||= Date.today
        entries = discover_pdfs(after, upto)

        dataset = []
        first = true
        entries.each do |date, url|
          sleep(0.5) unless first
          first = false

          pdf_data = http.get("#{HOST}#{url}").to_s
          next unless pdf_data.start_with?("%PDF")

          dataset.concat(parse(pdf_data, date))
        end

        dataset
      end

      def parse(pdf_data, date)
        reader = PDF::Reader.new(StringIO.new(pdf_data))
        text = reader.pages.map(&:text).join("\n")
        parse_text(text, date)
      end

      # The bulletin's only BSP-computed figure is the Reference Rate, its
      # official USD/PHP mid. Everything else in the PDF is a third-party
      # passthrough we deliberately skip (see the class comment).
      def parse_text(text, date)
        reference_rate = text[REFERENCE_RATE_PATTERN, 1]
        return [] unless reference_rate

        usd = Float(reference_rate.delete(","))
        return [] if usd.zero?

        [{ date: date, base: "USD", quote: "PHP", rate: usd }]
      end

      private

      # Enumerate the SharePoint list newest-first, collecting (date, pdf_url)
      # pairs whose bulletin date falls within [after, upto]. Stops paging once
      # an item predates the requested window.
      def discover_pdfs(after, upto)
        entries = {}
        url = "#{LIST_URL}?$top=#{PAGE_SIZE}&$orderby=Id+desc" \
          "&$expand=AttachmentFiles&$select=Id,Title,AttachmentFiles"

        loop do
          # SharePoint's REST API returns Atom/XML by default; the verbose OData Accept header is required to get JSON
          # back.
          body = http.get(url, headers: { "Accept" => "application/json;odata=verbose" }).to_s
          payload = JSON.parse(body)
          results = payload.dig("d", "results") || []
          break if results.empty?

          done = false
          results.each do |item|
            date = parse_title_date(item["Title"])
            next unless date

            if after && date < after
              done = true
              next
            end
            next if date > upto

            attachment = item.dig("AttachmentFiles", "results")&.first
            server_url = attachment && attachment["ServerRelativeUrl"]
            entries[date] ||= server_url if server_url
          end

          break if done

          next_link = payload.dig("d", "__next")
          break unless next_link

          url = next_link
        end

        entries.sort.to_a
      end

      def parse_title_date(title)
        Date.strptime(title.to_s.strip, "%d%b%Y")
      rescue ArgumentError
        nil
      end

      def read_timeout = 120
    end
  end
end
