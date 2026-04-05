# frozen_string_literal: true

require "net/http"

require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    # Bank Indonesia.
    # Fetches daily transaction exchange rates for 26 currencies against the Indonesian rupiah (IDR).
    # The page is SharePoint-based; we POST a search per currency and parse the HTML result table.
    # Rates are buy/sell; we compute the mid-rate as (sell + buy) / 2.
    class BI < Adapter
      BASE_URL = "https://www.bi.go.id/en/statistik/informasi-kurs/transaksi-bi/default.aspx"
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter/2.0; +https://frankfurter.dev)"

      class << self
        def backfill_range = 90
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        from_str = after.strftime("%d-%b-%Y")
        to_str = end_date.strftime("%d-%b-%Y")

        page = load_page
        prefix = extract_prefix(page)
        tokens = extract_tokens(page)
        cookies = @cookies
        currencies = extract_currencies(page)

        dataset = []
        first = true

        currencies.each do |currency|
          sleep(1) unless first
          first = false

          html = search(prefix:, tokens:, cookies:, currency:, from_str:, to_str:)
          dataset.concat(parse(html, currency:))
        end

        dataset
      end

      def parse(html, currency:)
        table = html[%r{gvSearchResult2"[^>]*>(.*?)</table>}mi, 1]
        return [] unless table

        records = []
        table.scan(%r{<tr[^>]*>(.*?)</tr>}mi) do |row_html,|
          cells = row_html.scan(%r{<td[^>]*>(.*?)</td>}mi).flatten.map { |c| c.strip.gsub(/<[^>]+>/, "").strip }
          next if cells.length < 4

          _value, sell_str, buy_str, date_str = cells
          sell = Float(sell_str.delete(","), exception: false)
          buy = Float(buy_str.delete(","), exception: false)
          next unless sell && buy && sell.positive? && buy.positive?

          mid = (sell + buy) / 2.0
          date = Date.parse(date_str)

          records << { date:, base: currency.strip, quote: "IDR", rate: mid.round(4) }
        end

        records
      end

      private

      def load_page
        uri = URI(BASE_URL)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = true
        http.open_timeout = 30
        http.read_timeout = 60
        req = Net::HTTP::Get.new(uri)
        req["User-Agent"] = USER_AGENT
        resp = http.request(req)
        @cookies = resp.get_fields("set-cookie")&.map { |c| c.split(";").first }&.join("; ") || ""
        resp.body
      end

      def search(prefix:, tokens:, cookies:, currency:, from_str:, to_str:)
        uri = URI(BASE_URL)
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = true
        http.open_timeout = 30
        http.read_timeout = 60

        form = sharepoint_fields(tokens).merge(
          "#{prefix}$ddlmatauang1" => currency,
          "#{prefix}$txtFrom" => from_str,
          "#{prefix}$txtTo" => to_str,
          "#{prefix}$txtTanggal" => "",
          "#{prefix}$btnSearch1" => "Search",
          "#{prefix}$hidSourceID" => "#{prefix.tr("$", "_")}_btnSearch1",
        )

        req = Net::HTTP::Post.new(uri)
        req["User-Agent"] = USER_AGENT
        req["Cookie"] = cookies
        req["Referer"] = BASE_URL
        req["Content-Type"] = "application/x-www-form-urlencoded"
        req.body = URI.encode_www_form(form)

        http.request(req).body
      end

      def extract_prefix(html)
        html[/name="(ctl00\$PlaceHolderMain\$[^"]*)\$txtFrom"/, 1]
      end

      def extract_tokens(html)
        {
          viewstate: html[/name="__VIEWSTATE"[^>]*value="([^"]*)"/, 1] || "",
          validation: html[/name="__EVENTVALIDATION"[^>]*value="([^"]*)"/, 1] || "",
          viewstategen: html[/name="__VIEWSTATEGENERATOR"[^>]*value="([^"]*)"/, 1] || "",
          requestdigest: html[/name="__REQUESTDIGEST"[^>]*value="([^"]*)"/, 1] || "",
        }
      end

      def extract_currencies(html)
        html.scan(%r{<select[^>]*ddlmatauang[^>]*>(.*?)</select>}mi).flatten
          .flat_map { |sel| sel.scan(/<option[^>]*value="([^"]*)"/) }
          .flatten
      end

      def sharepoint_fields(tokens)
        {
          "MSOWebPartPage_PostbackSource" => "",
          "MSOTlPn_View" => "0",
          "MSOTlPn_ShowSettings" => "False",
          "MSOTlPn_Button" => "none",
          "__EVENTTARGET" => "",
          "__EVENTARGUMENT" => "",
          "__REQUESTDIGEST" => tokens[:requestdigest],
          "MSOSPWebPartManager_DisplayModeName" => "Browse",
          "MSOSPWebPartManager_ExitingDesignMode" => "false",
          "MSOSPWebPartManager_OldDisplayModeName" => "Browse",
          "MSOSPWebPartManager_StartWebPartEditingName" => "false",
          "MSOSPWebPartManager_EndWebPartEditing" => "false",
          "_maintainWorkspaceScrollPosition" => "0",
          "__VIEWSTATE" => tokens[:viewstate],
          "__VIEWSTATEGENERATOR" => tokens[:viewstategen],
          "__SCROLLPOSITIONX" => "0",
          "__SCROLLPOSITIONY" => "0",
          "__EVENTVALIDATION" => tokens[:validation],
        }
      end
    end
  end
end
