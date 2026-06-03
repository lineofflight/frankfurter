# frozen_string_literal: true

# Rack middleware that bounds how long a single request may spend producing its
# response body. Long time-series exports stream rows one chunk at a time; left
# unbounded, a single slow request (or a flood of them) can pin a worker thread
# for minutes. This caps that to a wall-clock budget.
#
# The check is cooperative — it runs between body chunks rather than using
# Timeout.timeout or Thread#raise, both of which can fire in the middle of an
# arbitrary operation and corrupt state. Streaming bodies yield per row, so the
# deadline is enforced with fine granularity; a non-streaming body is a single
# chunk and effectively unaffected.
class RequestTimeout
  class RequestTimeoutError < StandardError; end

  # Sits just under Cloudflare's ~100s origin read timeout so the worker thread is freed a beat
  # before the edge gives up, while still allowing reasonably large (multi-year) exports to finish.
  # This is a safety backstop, not the answer for full-history requests — those run for minutes and
  # are meant to be served from the edge cache (or bounded at the caller). Tune via REQUEST_TIMEOUT_SECONDS.
  DEFAULT_SECONDS = Integer(ENV.fetch("REQUEST_TIMEOUT_SECONDS", 90))

  def initialize(app, seconds: DEFAULT_SECONDS)
    @app = app
    @seconds = seconds
  end

  def call(env)
    deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + @seconds
    status, headers, body = @app.call(env)
    [status, headers, TimedBody.new(body, deadline:, seconds: @seconds)]
  end

  # Wraps a Rack body, raising once the deadline passes. Releasing the raise lets
  # Puma close the connection and frees the worker thread (and any DB connection
  # checked out for the in-progress query) via the normal ensure path.
  class TimedBody
    def initialize(body, deadline:, seconds:)
      @body = body
      @deadline = deadline
      @seconds = seconds
    end

    def each
      @body.each do |chunk|
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) > @deadline
          raise RequestTimeoutError, "request exceeded #{@seconds}s timeout"
        end

        yield chunk
      end
    end

    def close
      @body.close if @body.respond_to?(:close)
    end
  end
end
