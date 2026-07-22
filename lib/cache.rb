# frozen_string_literal: true

require "net/http"

# Purges the CDN cache after data imports. Currently supports Cloudflare.
# No-ops when credentials are not configured.
class Cache
  DEBOUNCE_SECONDS = Integer(ENV.fetch("CACHE_PURGE_DEBOUNCE_SECONDS", 300))

  @mutex = Mutex.new
  @pending = false
  @last_purge_at = nil

  class << self
    def purge
      new.purge
    end

    # Leading edge of the purge debounce (#568): with ~50 providers publishing daily, a purge per
    # provider insert dumps the whole CDN cache many times a day, and a deploy's startup backfill
    # fires a burst of purges within minutes. The first insert of a quiet window purges immediately;
    # later inserts coalesce into a pending purge for purge_pending to flush.
    def purge_debounced
      fire = @mutex.synchronize do
        if window_open?
          @pending = true
          false
        else
          open_window!
          true
        end
      end
      attempt_purge if fire
    end

    # Trailing edge: flushes the coalesced purge once the window has expired, so the last insert of a
    # backfill wave always becomes visible. Driven externally (the scheduler's tick, or the end of a
    # rake backfill with ignore_window since the wave is over and the process is about to exit); safe
    # to call at any cadence. A failed purge is re-marked pending, so a later call retries it.
    def purge_pending(ignore_window: false)
      fire = @mutex.synchronize do
        next false unless @pending && (ignore_window || !window_open?)

        open_window!
        true
      end
      attempt_purge if fire
    end

    private

    # The window opens when a purge attempt starts, not when it completes, so callers arriving while
    # the HTTP call is in flight coalesce into pending instead of firing concurrently.
    def open_window!
      @pending = false
      @last_purge_at = monotonic
    end

    def attempt_purge
      purge
    rescue StandardError
      @mutex.synchronize { @pending = true }
      raise
    end

    def window_open?
      @last_purge_at && monotonic - @last_purge_at < DEBOUNCE_SECONDS
    end

    def monotonic
      Process.clock_gettime(Process::CLOCK_MONOTONIC)
    end
  end

  attr_reader :zone_id, :api_token

  def initialize(zone_id: ENV["CLOUDFLARE_ZONE_ID"], api_token: ENV["CLOUDFLARE_API_TOKEN"])
    @zone_id = zone_id
    @api_token = api_token
  end

  def purge
    return unless configured?

    uri = URI("https://api.cloudflare.com/client/v4/zones/#{zone_id}/purge_cache")
    req = Net::HTTP::Post.new(uri)
    req["Authorization"] = "Bearer #{api_token}"
    req["Content-Type"] = "application/json"
    req.body = '{"purge_everything":true}'

    res = Net::HTTP.start(uri.hostname, uri.port, use_ssl: true) { |http| http.request(req) }
    # Raise on non-2xx so a rejected purge counts as a failure and stays pending for retry.
    res.value
  end

  private

  def configured?
    zone_id && api_token
  end
end
