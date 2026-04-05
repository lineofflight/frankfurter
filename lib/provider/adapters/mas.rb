# frozen_string_literal: true

require "csv"
require "net/http"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Monetary Authority of Singapore. Publishes daily exchange rates for 20
    # currencies against the Singapore dollar (SGD) via a statistics page that
    # serves CSV downloads. Rates are quoted as SGD per unit (or per 100 units)
    # of foreign currency. Data available from 1988.
    class MAS < Adapter
      URL = "https://eservices.mas.gov.sg/statistics/msb/ExchangeRates.aspx"

      # Column header patterns mapped to ISO currency codes and units.
      # Per-unit currencies have unit=1, per-100-unit currencies have unit=100.
      COLUMNS = {
        "Euro" => { code: "EUR", unit: 1 },
        "Pound Sterling" => { code: "GBP", unit: 1 },
        "US Dollar" => { code: "USD", unit: 1 },
        "Australian Dollar" => { code: "AUD", unit: 100 },
        "Canadian Dollar" => { code: "CAD", unit: 100 },
        "Chinese Renminbi" => { code: "CNY", unit: 100 },
        "Hong Kong Dollar" => { code: "HKD", unit: 100 },
        "Indian Rupee" => { code: "INR", unit: 100 },
        "Indonesian Rupiah" => { code: "IDR", unit: 100 },
        "Japanese Yen" => { code: "JPY", unit: 100 },
        "Korean Won" => { code: "KRW", unit: 100 },
        "Malaysian Ringgit" => { code: "MYR", unit: 100 },
        "New Taiwan Dollar" => { code: "TWD", unit: 100 },
        "New Zealand Dollar" => { code: "NZD", unit: 100 },
        "Philippine Peso" => { code: "PHP", unit: 100 },
        "Qatar Riyal" => { code: "QAR", unit: 100 },
        "Saudi Arabia Riyal" => { code: "SAR", unit: 100 },
        "Swiss Franc" => { code: "CHF", unit: 100 },
        "Thai Baht" => { code: "THB", unit: 100 },
        "UAE Dirham" => { code: "AED", unit: 100 },
        "Vietnamese Dong" => { code: "VND", unit: 100 },
      }.freeze

      class << self
        def backfill_range = 365
      end

      def fetch(after: nil, upto: nil)
        end_date = upto || Date.today
        dataset = []

        each_year(after, end_date) do |start_month, start_year, end_month, end_year|
          csv = download_csv(start_month, start_year, end_month, end_year)
          dataset.concat(parse(csv))
          sleep(1)
        end

        dataset.select! { |r| r[:date] >= after } if after
        dataset.select! { |r| r[:date] <= end_date }
        dataset
      end

      def parse(csv)
        lines = csv.lines
        header_index = lines.index { |l| l.start_with?("End of Period") }
        return [] unless header_index

        header_line = lines[header_index]
        columns = parse_header(header_line)
        return [] if columns.empty?

        data_lines = lines[(header_index + 1)..]
        records = []
        current_year = nil
        current_month = nil

        data_lines.each do |line|
          row = CSV.parse_line(line)
          next unless row && row.length > 3

          current_year = row[0].strip.to_i if row[0] && !row[0].strip.empty?
          current_month = parse_month(row[1].strip) if row[1] && !row[1].strip.empty?
          day = row[2]&.strip&.to_i
          next unless current_year&.positive? && current_month&.positive? && day&.positive?

          date = Date.new(current_year, current_month, day)

          columns.each do |col_index, meta|
            value = row[col_index]&.strip
            next if value.nil? || value.empty?

            rate = Float(value)
            next if rate.zero?

            records << { date:, base: meta[:code], quote: "SGD", rate: rate / meta[:unit] }
          rescue ArgumentError
            next
          end
        end

        records
      end

      private

      def download_csv(start_month, start_year, end_month, end_year)
        uri = URI(URL)

        # Step 1: GET page to extract ASP.NET tokens and cookies
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = true

        get_response = http.get(uri.request_uri)
        cookies = extract_cookies(get_response)
        tokens = extract_tokens(get_response.body)

        # Step 2: POST to download CSV
        form_data = tokens.merge(
          "ctl00$ContentPlaceHolder1$StartYearDropDownList" => start_year.to_s,
          "ctl00$ContentPlaceHolder1$EndYearDropDownList" => end_year.to_s,
          "ctl00$ContentPlaceHolder1$StartMonthDropDownList" => start_month.to_s,
          "ctl00$ContentPlaceHolder1$EndMonthDropDownList" => end_month.to_s,
          "ctl00$ContentPlaceHolder1$FrequencyDropDownList" => "D",
          "ctl00$ContentPlaceHolder1$DownloadButton" => "Download",
        )

        # Check all currency checkboxes
        3.times { |i| form_data["ctl00$ContentPlaceHolder1$EndOfPeriodPerUnitCheckBoxList$#{i}"] = "on" }
        18.times { |i| form_data["ctl00$ContentPlaceHolder1$EndOfPeriodPer100UnitsCheckBoxList$#{i}"] = "on" }

        post = Net::HTTP::Post.new(uri.request_uri)
        post["Cookie"] = cookies
        post.set_form_data(form_data)

        response = http.request(post)
        response.body
      end

      def extract_cookies(response)
        cookies = []
        response.get_fields("set-cookie")&.each do |cookie|
          cookies << cookie.split(";").first
        end
        cookies.join("; ")
      end

      def extract_tokens(html)
        tokens = {}
        ["__VIEWSTATE", "__VIEWSTATEGENERATOR", "__EVENTVALIDATION"].each do |name|
          match = html.match(/name="#{name}"[^>]*value="([^"]*)"/)
          tokens[name] = match[1] if match
        end
        tokens
      end

      def parse_header(header_line)
        columns = {}
        parts = CSV.parse_line(header_line)
        parts.each_with_index do |col, index|
          next unless col

          COLUMNS.each do |name, meta|
            if col.include?(name)
              columns[index] = meta
              break
            end
          end
        end
        columns
      end

      MONTHS = {
        "Jan" => 1,
        "Feb" => 2,
        "Mar" => 3,
        "Apr" => 4,
        "May" => 5,
        "Jun" => 6,
        "Jul" => 7,
        "Aug" => 8,
        "Sep" => 9,
        "Oct" => 10,
        "Nov" => 11,
        "Dec" => 12,
      }.freeze

      def parse_month(str)
        MONTHS[str]
      end

      def each_year(start_date, end_date)
        date = start_date
        while date <= end_date
          year_end = Date.new(date.year, 12, 31)
          chunk_end = [year_end, end_date].min

          start_month = date.month
          end_month = chunk_end.month

          yield start_month, date.year, end_month, chunk_end.year

          date = year_end + 1
        end
      end
    end
  end
end
