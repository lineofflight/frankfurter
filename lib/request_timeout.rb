# frozen_string_literal: true

class RequestTimeout
  class Error < StandardError; end

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

  class TimedBody
    def initialize(body, deadline:, seconds:)
      @body = body
      @deadline = deadline
      @seconds = seconds
    end

    def each
      @body.each do |chunk|
        if Process.clock_gettime(Process::CLOCK_MONOTONIC) > @deadline
          raise Error, "request exceeded #{@seconds}s timeout"
        end

        yield chunk
      end
    end

    def close
      @body.close if @body.respond_to?(:close)
    end
  end
end
