# frozen_string_literal: true

require "bigdecimal"
require "http"

class Provider < Sequel::Model(:providers)
  module Adapters
    class Adapter
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter; +https://frankfurter.dev)"

      # ISO 4217 defines XAU/XAG/XPT/XPD as one troy ounce. Adapters whose source publishes precious-metal rates per
      # gram multiply by this to convert into the per-ounce convention used across the app.
      GRAMS_PER_TROY_OUNCE = 31.1034768

      # Raises on any response that is not 2xx. Stricter than http.rb's built-in raise_error feature (>= 400 only): a
      # redirect from a moved or retired page must fail loudly, not parse as an empty day. 429 passes through so the
      # client's retriable layer can honor Retry-After; exhaustion raises HTTP::OutOfRetriesError, so no 429 reaches an
      # adapter either.
      class EnsureSuccess < HTTP::Feature
        def initialize(ignore: [])
          super()
          @ignore = ignore
        end

        def wrap_response(response)
          return response if response.status.success? || @ignore.include?(response.code)

          raise HTTP::StatusError, response
        end

        HTTP::Options.register_feature(:ensure_success, self)
      end

      class << self
        def inherited(subclass)
          super
          subclass.define_method(:sleep) { |*| nil } if ENV["APP_ENV"] == "test"
        end

        def backfill_range = nil

        def fetch_each(after: nil)
          return if after && after >= Date.today

          loop do
            upto = after + backfill_range - 1 if after && backfill_range
            upto = nil if upto && upto >= Date.today
            records = new.fetch(after:, upto:)
            yield records if records.any?
            break unless upto

            after = upto + 1
          end
        end
      end

      def fetch(after: nil, upto: nil)
        raise NotImplementedError
      end

      private

      # Many sources publish a buy and a sell price rather than a reference rate, so the mid is our own synthesis, with
      # no published digits of its own to echo. Binary floats leave noise at the bottom of it, because the error is in
      # the operands before the halving even starts:
      #
      #   (181.5264 + 181.76) / 2.0 => 181.64319999999998
      #
      # Halving a terminating decimal always terminates (x / 2 == x * 5 / 10), so decimal arithmetic gives the mid
      # exactly: one digit deeper than its inputs, and nothing beyond. RatePrecision is the backstop at ingest; this
      # keeps the value right at the source.
      def midpoint(buy, sell)
        ((BigDecimal(buy.to_s) + BigDecimal(sell.to_s)) / 2).to_f
      end

      # http.rb sends no Accept header of its own, and a request without one is a bot fingerprint some WAFs reject.
      # CBE's did, silently: HTTP 200 with a "Request Rejected" body, so nothing raised and the feed just went stale
      # (#576). `*/*` means what the absent header already meant, and adapters needing a specific type override it.
      def http
        @http ||= HTTP
          .use(ensure_success: { ignore: [429] })
          .retriable(retry_statuses: [429])
          .timeout(connect: 10, write: 60, read: 120)
          .headers("User-Agent" => USER_AGENT, "Accept" => "*/*")
      end
    end
  end
end
