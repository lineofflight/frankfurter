# frozen_string_literal: true

class Provider < Sequel::Model(:providers)
  module Adapters
    class Adapter
      class Unavailable < StandardError
      end

      # ISO 4217 defines XAU/XAG/XPT/XPD as one troy ounce. Adapters whose
      # source publishes precious-metal rates per gram multiply by this to
      # convert into the per-ounce convention used across the app.
      GRAMS_PER_TROY_OUNCE = 31.1034768

      TRANSIENT_ERRORS = [
        Errno::ECONNRESET,
        Errno::EPIPE,
        Net::OpenTimeout,
        Net::ReadTimeout,
        OpenSSL::SSL::SSLError,
        SocketError,
      ].freeze

      class << self
        def inherited(subclass)
          super
          subclass.define_method(:sleep) { |*| nil } if ENV["APP_ENV"] == "test"
        end

        def backfill_range = nil

        def fetch_each(after: nil)
          return if after && after >= Date.today

          retries = 0
          loop do
            upto = after + backfill_range - 1 if after && backfill_range
            upto = nil if upto && upto >= Date.today
            records = new.fetch(after:, upto:)
            yield records if records.any?
            retries = 0
            break unless upto

            after = upto + 1
          rescue *TRANSIENT_ERRORS
            retries += 1
            raise if retries > 5

            sleep(2**retries)
            retry
          end
        end
      end

      def fetch(after: nil, upto: nil)
        raise NotImplementedError
      end
    end
  end
end
