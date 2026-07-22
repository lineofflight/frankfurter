# frozen_string_literal: true

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

      def http
        @http ||= HTTP
          .use(ensure_success: { ignore: [429] })
          .retriable(retry_statuses: [429])
          .timeout(connect: 10, write: 60, read: read_timeout)
          .headers("User-Agent" => USER_AGENT)
      end

      def read_timeout = 60
    end
  end
end
