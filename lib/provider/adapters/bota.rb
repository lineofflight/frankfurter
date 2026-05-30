# frozen_string_literal: true

require "net/http"
require "ox"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Bank of Tanzania. Publishes daily exchange rates for 35+ currencies
    # against the Tanzanian shilling (TZS). Uses the Mean column (midpoint
    # of buy/sell). Publishes 7 days a week.
    #
    # The previous_rates endpoint is protected by ASP.NET MVC antiforgery
    # validation: a POST must carry a __RequestVerificationToken both as a
    # cookie and as a matching form field, or the server returns HTTP 500.
    # fetch() first GETs the page to obtain the session cookie and scrape the
    # hidden token field, then POSTs the date range with the cookie and token.
    class BOTA < Adapter
      BASE_URL = "https://www.bot.go.tz"
      EXCLUDED_CURRENCIES = ["GOLD", "ATS", "NLG", "MZM", "ZWD", "CUC"].freeze
      TOKEN_FIELD = "__RequestVerificationToken"
      TOKEN_PATTERN = /name="#{TOKEN_FIELD}"[^>]*value="([^"]*)"/

      class << self
        def backfill_range = 30
      end

      def fetch(after: nil, upto: nil)
        uri = URI("#{BASE_URL}/ExchangeRate/previous_rates")

        Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https") do |http|
          get = http.request(Net::HTTP::Get.new(uri))
          cookie = cookie_header(get)
          token = extract_token(get.body)

          post = Net::HTTP::Post.new(uri)
          post["Cookie"] = cookie if cookie
          post.set_form_data(
            TOKEN_FIELD => token,
            "dateFrom" => after.strftime("%m/%d/%Y"),
            "dateTo" => (upto || Date.today).strftime("%m/%d/%Y"),
          )
          response = http.request(post)

          sleep(2)
          parse(response.body)
        end
      end

      def parse(html)
        doc = Ox.load(html, mode: :generic, effort: :tolerant, smart: true)
        rows = find_table_rows(doc)
        return [] unless rows

        rows.filter_map { |row| parse_row(row) }
      end

      private

      def cookie_header(response)
        cookies = response.get_fields("set-cookie")
        return unless cookies

        cookies.map { |c| c.split(";", 2).first }.join("; ")
      end

      def extract_token(html)
        match = html.match(TOKEN_PATTERN)
        match && match[1]
      end

      def find_table_rows(node)
        return unless node.respond_to?(:nodes)

        if node.respond_to?(:value) && node.value == "tbody"
          return node.nodes&.select { |n| n.respond_to?(:value) && n.value == "tr" }
        end

        node.nodes&.each do |child|
          result = find_table_rows(child)
          return result if result
        end

        nil
      end

      def parse_row(row)
        cells = row.nodes&.select { |n| n.respond_to?(:value) && n.value == "td" }
        return unless cells && cells.size >= 6

        currency = cell_text(cells[1])&.strip&.upcase
        return unless currency
        return if EXCLUDED_CURRENCIES.include?(currency)

        mean_str = cell_text(cells[4])
        date_str = cell_text(cells[5])
        return unless mean_str && date_str

        rate = Float(mean_str.tr(",", ""))
        return if rate.zero?

        date = Date.parse(date_str.strip)

        { date:, base: currency, quote: "TZS", rate: }
      end

      def cell_text(node)
        return unless node

        texts = []
        collect_text(node, texts)
        text = texts.join
        text.empty? ? nil : text
      end

      def collect_text(node, texts)
        if node.is_a?(String)
          texts << node
        elsif node.respond_to?(:nodes) && node.nodes
          node.nodes.each { |child| collect_text(child, texts) }
        end
      end
    end
  end
end
