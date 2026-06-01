# frozen_string_literal: true

require "date"
require "json"
require "net/http"
require "pdf-reader"
require "stringio"
require "uri"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bangko Sentral ng Pilipinas — the daily Reference Exchange Rate Bulletin
    # (RERB), published every business day (Mon-Fri, no weekends or Philippine
    # holidays). Each bulletin is a single PDF listing ~32 currencies with a
    # "PHIL. PESO EQUIVALENT" column — Philippine pesos per one unit of the
    # foreign currency.
    #
    # The bulletins live in a SharePoint list named "RERB", exposed without
    # authentication via the SharePoint REST API. Each list item carries a
    # Title holding the bulletin date as DDMMMYYYY (e.g. "29May2026") and an
    # attachment — the PDF — reachable at its ServerRelativeUrl. We enumerate
    # the list (newest first), collect items whose date falls in the requested
    # window, fetch each PDF, and parse the fixed-layout table with pdf-reader.
    #
    # Direction: foreign currency in base, PHP in quote (1 foreign = X PHP),
    # matching the convention used by other pivot-in-quote adapters (e.g. NBG,
    # BBK, BRB).
    #
    # USD is special-cased: the bulletin's USD row carries a Reuters-derived
    # peso equivalent, but BSP also publishes a "BSP Reference Rate" — its
    # official USD/PHP mid. We emit that mid for USD rather than the buying or
    # selling T/T rates, sidestepping buy/sell direction issues (cf. issue #314).
    #
    # The bulletin also lists an SDR rate ("SDR Rate: $ X /SDR") and gold/silver
    # buying prices ("GOLD BUYING: $ X", "SILVER BUYING: $ X", a Reuters/LBMA
    # passthrough). These are quoted against the dollar rather than the peso, so
    # we emit them in their native USD direction instead of as PHP quotes:
    #   - SDR  "$ X /SDR"   → 1 XDR = X USD → { base: "XDR", quote: "USD" }
    #   - gold "GOLD ... $ X" (USD per troy ounce)   → { base: "XAU", quote: "USD" }
    #   - silver "SILVER ... $ X" (USD per troy ounce) → { base: "XAG", quote: "USD" }
    # Metals stay metal-in-base per troy ounce, matching the convention used by
    # other metal adapters (CBSL, CBI, BOM). The figures are already per troy
    # ounce, so no per-gram conversion. XDR is also published authoritatively by
    # the IMF, so BSP's quote is a redundant cross-check.
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

      # A data row: <num> <COUNTRY...> <UNIT[*...]> <SYMBOL> <EURO eq> <USD eq> <PHP eq>.
      # Anchor on the 3-letter ISO symbol followed by three value columns (a
      # number or "N/A"); the last column is the peso equivalent.
      ROW_PATTERN = %r{\A\s*\d+\s+.+?\s+([A-Z]{3})\s+(?:N/A|[\d.,]+)\s+(?:N/A|[\d.,]+)\s+(N/A|[\d.,]+)\s*\z}
      REFERENCE_RATE_PATTERN = /BSP Reference Rate:\s*PHP\s+([\d.,]+)/

      # USD-denominated extras: 1 SDR = X USD; gold/silver buying prices in USD
      # per troy ounce. Each appears once per bulletin.
      SDR_PATTERN = %r{SDR Rate:\s*\$\s*([\d.,]+)\s*/SDR}
      GOLD_PATTERN = /GOLD BUYING:\s*\$\s*([\d.,]+)/
      SILVER_PATTERN = /SILVER BUYING:\s*\$\s*([\d.,]+)/

      class << self
        # Each request fetches a single day's PDF, so chunk the archive to keep
        # progress durable — fetch_each upserts after every window.
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

          pdf_data = http_get(URI("#{HOST}#{url}"))
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

      def parse_text(text, date)
        reference_rate = text[REFERENCE_RATE_PATTERN, 1]

        records = text.each_line.filter_map do |line|
          match = line.match(ROW_PATTERN)
          next unless match

          code = match[1]
          # USD uses the official BSP Reference Rate (mid), handled separately.
          next if code == "USD"

          peso = match[2]
          next if peso == "N/A"

          rate = Float(peso.delete(","))
          next if rate.zero?

          { date: date, base: code, quote: "PHP", rate: rate }
        end

        if reference_rate
          usd = Float(reference_rate.delete(","))
          records << { date: date, base: "USD", quote: "PHP", rate: usd } if usd.nonzero?
        end

        # USD-denominated extras, emitted in their native direction.
        add_usd_rate(records, date, "XDR", text[SDR_PATTERN, 1])
        add_usd_rate(records, date, "XAU", text[GOLD_PATTERN, 1])
        add_usd_rate(records, date, "XAG", text[SILVER_PATTERN, 1])

        records
      end

      private

      # Append a USD-quoted record (base in USD per unit) when the value parses
      # to a non-zero number.
      def add_usd_rate(records, date, base, value)
        return unless value

        rate = Float(value.delete(","))
        records << { date: date, base: base, quote: "USD", rate: rate } if rate.nonzero?
      end

      # Enumerate the SharePoint list newest-first, collecting (date, pdf_url)
      # pairs whose bulletin date falls within [after, upto]. Stops paging once
      # an item predates the requested window.
      def discover_pdfs(after, upto)
        entries = {}
        url = "#{LIST_URL}?$top=#{PAGE_SIZE}&$orderby=Id+desc" \
          "&$expand=AttachmentFiles&$select=Id,Title,AttachmentFiles"

        loop do
          body = http_get(URI(url), accept: "application/json;odata=verbose")
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

      def http_get(uri, accept: nil)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = (uri.scheme == "https")
        http.open_timeout = 30
        http.read_timeout = 120

        request = Net::HTTP::Get.new(uri.request_uri)
        request["Accept"] = accept if accept

        response = http.request(request)
        response.value
        response.body
      end
    end
  end
end
