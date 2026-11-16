# frozen_string_literal: true

require "bigdecimal"
require "json"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Board of Indirect Taxes and Customs (India). Notifies customs exchange rates under Section 14 of the
    # Customs Act on the evening of the first and third Thursday of each month, with amendments in between, through
    # ICEGATE's Exchange Rate Automation Module since July 2024. Each notification gives INR per 1 or 100 foreign units
    # for imports (CBIC.IMPORT) and exports (CBIC.EXPORT), in force from the next day until the next notification. Not
    # daily, so these never blend.
    class CBIC < Adapter
      API_URL = "https://foservices.icegate.gov.in/cbu/icegateapi"
      SERIES = { "CBIC.IMPORT" => "cbicImport", "CBIC.EXPORT" => "cbicExport" }.freeze

      # Reads after as exclusive: the latest stored notification is not refetched on every poll.
      def fetch(after: nil, upto: nil)
        # The list dates a notification on its publish day, and it is in force from the next.
        numbers = notifications.filter_map do |number, published|
          effective = published + 1
          number unless (after && effective <= after) || (upto && effective > upto)
        end

        numbers.flat_map.with_index do |number, index|
          sleep(0.5) if index.positive?
          parse(http.post("#{API_URL}/igexratepublishnot", json: { notNum: number }).to_s)
        end
      end

      def parse(json)
        data = JSON.parse(json)
        raise "CBIC: notification #{data["notificationNumber"]}: #{data["error"]}" if data["error"]

        # notPublishDate here is the date the notification takes effect, which ICEGATE's own PDF prints as w.e.f.
        date = Date.strptime(data.fetch("notPublishDate"), "%d-%m-%Y")
        data.fetch("currencyDetail").flat_map do |row|
          units = BigDecimal(row["units"].to_s)
          next [] unless units.positive?

          SERIES.filter_map do |dataset, field|
            value = row[field]
            next if value.nil?

            { dataset:, date:, base: row["currencyCode"], quote: "INR", rate: BigDecimal(value.to_s) / units }
          end
        end
      end

      private

      def notifications
        list = JSON.parse(http.post("#{API_URL}/getnotdetails").to_s)
        raise "CBIC: empty notification list" if list.empty?

        list.map do |entry|
          [entry.fetch("notificationNumber"), Date.strptime(entry.fetch("notPublishDate"), "%d-%m-%Y")]
        end
      end
    end
  end
end
