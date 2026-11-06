# frozen_string_literal: true

require "bigdecimal"
require "http/cookie"
require "http"
require "openssl"
require "rate_components"
require "socket"
require "timeout"
require "web_bot_auth"

class Provider < Sequel::Model(:providers)
  module Adapters
    class Adapter
      USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter; +https://github.com/lineofflight/frankfurter)"

      # ISO 4217 defines XAU/XAG/XPT/XPD as one troy ounce. Adapters whose source publishes precious-metal rates per
      # gram multiply by this to convert into the per-ounce convention used across the app.
      GRAMS_PER_TROY_OUNCE = 31.1034768

      # Sources often label a redenominated currency's whole history with its current ISO code without restating the
      # values, so the old rows carry the predecessor's magnitudes under the successor's code. An adapter that sees this
      # overrides PREDECESSORS, mapping the current code to its predecessor and the first date the source's values are
      # in the successor unit, which can trail the official date. historical_code applies the map per row.
      PREDECESSORS = {}.freeze

      # The reverse: a source that keeps a retired code after a redenomination and quotes the successor's values under
      # it. SUCCESSORS maps the old code to its successor and the first date the source's values are in the new unit. A
      # successor that is itself retired chains to its own entry.
      SUCCESSORS = {}.freeze

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

      # Some provider sites serve their leaf certificate without the intermediate that issued it (or alongside a stale
      # one), so OpenSSL fails with "unable to get local issuer certificate". Browsers fetch the missing link on the fly
      # from the leaf's Authority Information Access (CA Issuers) extension; OpenSSL does not. When a handshake fails
      # for a missing issuer, read the peer's leaf, walk its CA Issuers links, and add the fetched intermediates to
      # Ruby's default trust store once OpenSSL confirms the chain reaches a system root.
      class CompleteChain < HTTP::Feature
        ISSUER_ERROR = /unable to get (?:local )?issuer certificate/
        CA_ISSUERS_URI = %r{CA Issuers - URI:(https?://\S+)}
        MAX_DEPTH = 3
        TIMEOUT = 10
        MUTEX = Mutex.new

        def initialize(store: OpenSSL::SSL::SSLContext::DEFAULT_CERT_STORE)
          super()
          @store = store
        end

        def around_request(request)
          chased = false
          begin
            yield request
          rescue OpenSSL::SSL::SSLError => e
            raise if chased || !e.message.match?(ISSUER_ERROR) || !complete_chain?(request.uri)

            chased = true
            retry
          end
        end

        private

        def complete_chain?(uri)
          return false unless uri.https?

          leaf = fetch_leaf(uri.host, uri.port || 443)
          return false unless leaf

          certs = []
          seen = [leaf.to_der]
          current = leaf
          MAX_DEPTH.times do
            fetched = fetch_issuers(current)
            break if fetched.empty?

            fetched.each { |c| certs << c unless certs.any? { |existing| existing.to_der == c.to_der } }
            advanced = false
            while (issuer = certs.find { |c| !seen.include?(c.to_der) && issued_by?(current, c) })
              seen << issuer.to_der
              current = issuer
              advanced = true
            end
            break unless advanced

            next unless (intermediates = verified_intermediates(leaf, certs))

            MUTEX.synchronize do
              intermediates.each { |cert| add_cert(cert) }
            end
            return true
          end

          false
        end

        def fetch_leaf(host, port)
          Socket.tcp(host, port, connect_timeout: TIMEOUT) do |tcp|
            context = OpenSSL::SSL::SSLContext.new
            context.verify_mode = OpenSSL::SSL::VERIFY_NONE
            ssl = OpenSSL::SSL::SSLSocket.new(tcp, context)
            ssl.hostname = host
            Timeout.timeout(TIMEOUT) { ssl.connect }
            ssl.peer_cert
          ensure
            ssl&.close
          end
        rescue StandardError
          nil
        end

        def fetch_issuers(cert)
          ext = cert.extensions.find { |e| e.oid == "authorityInfoAccess" }
          url = ext&.value&.slice(CA_ISSUERS_URI, 1)
          return [] unless url

          response = HTTP.timeout(connect: TIMEOUT, write: TIMEOUT, read: TIMEOUT).follow(max_hops: 3).get(url)
          return [] unless response.status.success?

          parse_certificates(response.to_s)
        rescue StandardError
          []
        end

        def parse_certificates(bytes)
          OpenSSL::X509::Certificate.load(bytes)
        rescue OpenSSL::X509::CertificateError
          OpenSSL::PKCS7.new(bytes).certificates || []
        end

        def issued_by?(child, issuer)
          child.issuer == issuer.subject && child.verify(issuer.public_key)
        rescue OpenSSL::X509::CertificateError, OpenSSL::PKey::PKeyError
          false
        end

        def verified_intermediates(leaf, certs)
          ctx = OpenSSL::X509::StoreContext.new(@store, leaf, certs)
          return unless ctx.verify

          intermediates = ctx.chain&.slice(1..-2)
          intermediates if intermediates&.any?
        end

        def add_cert(cert)
          @store.add_cert(cert)
        rescue OpenSSL::X509::StoreError
          # Already in store.
        end

        HTTP::Options.register_feature(:complete_chain, self)
      end

      class << self
        def inherited(subclass)
          super
          subclass.define_method(:sleep) { |*| nil } if ENV["APP_ENV"] == "test"
        end

        def backfill_range = nil

        # True for a source that may replace an already-published value in place (HMRC can correct a monthly customs
        # rate mid-month). Backfill is insert-only, so such a correction would otherwise pass unnoticed; flagging the
        # adapter makes backfill compare fetched rows against stored ones and warn on drift.
        def revises? = false

        # How many days ahead of the fetch a row may legitimately be dated. Most sources publish for today or the next
        # business day, which the universal grace window covers; a source that publishes a rate before its period starts
        # (HMRC: the coming month's customs rates) declares the lead so validation keeps the rows.
        def lead_days = 0

        def fetch_each(after: nil)
          return if after && after >= Date.today

          loop do
            upto = after + backfill_range - 1 if after && backfill_range
            upto = nil if upto && upto >= Date.today
            records = new.fetch(after:, upto:)
            yield records if records.any?
            break unless upto

            # Step to the window's last day, not past it: an adapter that reads after as exclusive would otherwise never
            # request upto + 1. One that reads it as inclusive refetches that day, which the insert skips. A one-day
            # window has no overlap to give.
            after = backfill_range > 1 ? upto : upto + 1
          end
        end
      end

      def fetch(after: nil, upto: nil)
        raise NotImplementedError
      end

      private

      def historical_code(code, date)
        predecessor, cutover = self.class::PREDECESSORS[code]
        return predecessor if predecessor && date < cutover

        loop do
          successor, cutover = self.class::SUCCESSORS[code]
          return code unless successor && date >= cutover

          code = successor
        end
      end

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
        RateComponents.midpoint(buy, sell)
      end

      # Preserve the published components in the same per-unit direction as the rate. The existing rate calculation
      # remains the compatibility oracle while the SQL resolver is evaluated against recorded and historical data.
      def prices(bid:, ask:, mid: nil, unit: 1)
        { bid:, ask:, mid: }.transform_values do |value|
          (BigDecimal(value.to_s) / unit).to_f unless value.nil?
        end
      end

      # http.rb sends no Accept header of its own, and a request without one is a bot fingerprint some WAFs reject.
      # CBE's did, silently: HTTP 200 with a "Request Rejected" body, so nothing raised and the feed just went stale
      # (#576). `*/*` means what the absent header already meant, and adapters needing a specific type override it.
      def http
        @http ||= HTTP
          .use(:complete_chain, :web_bot_auth, ensure_success: { ignore: [429] })
          .retriable(retry_statuses: [429])
          .timeout(connect: 10, write: 60, read: 120)
          .headers("User-Agent" => WebBotAuth.ours? ? WebBotAuth::USER_AGENT : USER_AGENT, "Accept" => "*/*")
      end
    end
  end
end
