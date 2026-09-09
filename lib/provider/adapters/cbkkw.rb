# frozen_string_literal: true

require "bigdecimal"

require "provider/adapters/adapter"

class Provider
  module Adapters
    # Central Bank of Kuwait. Publishes daily reference rates for ~135 currencies against KWD, quoted in fils (one
    # thousandth of a dinar) per unit of foreign currency: "KWD / US Dollar 306.650" means 1 USD = 0.30665 KWD. Foreign
    # is the base and KWD the quote (pivot-in-quote, as NBG and BBK); the fils figure is divided by 1000 here.
    #
    # The exchange-rates page carries a lookup form whose currency select pairs each ISO code with a CMS id
    # ("USD:128735"). The endpoint keys on the id, and a bare code returns nothing, so the list is scraped from the page
    # rather than hard-coded. One POST per currency then returns an HTML fragment with a date-filtered table (dates
    # DD.MM.YYYY, working days Sun-Thu). The site sits behind F5 BIG-IP but serves plain requests without a cookie leg.
    # Majors run from 2008-01-02, most other currencies from 2017-06-18. Some retired codes (ECS, VEF, SLL) are still
    # served; RateValidation drops them.
    #
    # Licence: the CBK disclaimer (see terms_url) allows use "provided that they shall be properly credited to the CBK",
    # with written-permission boilerplate for everything else. Ships under the #611 rule with attribution and a courtesy
    # notice. Cite the Central Bank of Kuwait as the source.
    class CBKKW < Adapter
      BASE_URL = "https://www.cbk.gov.kw/en/monetary-policy/market-operations/exchange-rates"
      FORM_URL = "#{BASE_URL}/usd".freeze
      LOOKUP_URL = "#{BASE_URL}/get-exchange-rates".freeze
      FORM_ID_PATTERN = /name="formId"[^>]*value="(\d+)"/
      OPTION_PATTERN = /<option value="([A-Z]{3}):(\d+)"/
      ROW_PATTERN = %r{<tr>\s*<td>(\d{2}\.\d{2}\.\d{4})</td>\s*<td>([\d.]+)</td>\s*</tr>}
      FRAGMENT_MARKER = 'id="currencyJSON"'
      FILS_PER_DINAR = 1000

      def fetch(after: nil, upto: nil)
        start_date = after || Date.new(2008, 1, 2)
        end_date = upto || Date.today
        return [] if start_date > end_date

        form_id, currencies = parse_form(http.get(FORM_URL).to_s)

        currencies.flat_map.with_index do |(code, id), index|
          sleep(0.5) unless index.zero?
          parse(lookup(form_id, code, id, start_date, end_date), code)
        end
      end

      # Returns [form_id, { "USD" => "128735", ... }] from the lookup form page.
      def parse_form(html)
        form_id = html[FORM_ID_PATTERN, 1]
        raise "CBKKW: formId not found on #{FORM_URL}" unless form_id

        currencies = html.scan(OPTION_PATTERN).to_h
        raise "CBKKW: no currency options on #{FORM_URL}" if currencies.empty?

        [form_id, currencies]
      end

      # Parses the lookup fragment for one currency. An empty table is a genuine no-data window (weekend, retired code);
      # a body without the fragment marker is a WAF or error page and raises.
      def parse(html, code)
        raise "CBKKW: unexpected response for #{code}" unless html.include?(FRAGMENT_MARKER)

        html.scan(ROW_PATTERN).filter_map do |date_str, fils|
          rate = (BigDecimal(fils) / FILS_PER_DINAR).to_f
          next if rate.zero?

          { date: Date.strptime(date_str, "%d.%m.%Y"), base: code, quote: "KWD", rate: }
        end
      end

      private

      def lookup(form_id, code, id, start_date, end_date)
        # txtDateFrom is exclusive and txtDateTo inclusive, so the window starts a day early.
        form = {
          "formId" => form_id,
          "selCurrency" => "#{code}:#{id}",
          "txtDateFrom" => (start_date - 1).strftime("%d/%m/%Y"),
          "txtDateTo" => end_date.strftime("%d/%m/%Y"),
        }

        http.post(LOOKUP_URL, form:).to_s
      end
    end
  end
end
