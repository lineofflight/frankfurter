# frozen_string_literal: true

# Rack middleware that prevents CDNs and caches from holding onto error
# responses. Without this, an error inheriting the success path's
# `Cache-Control: public, max-age=86400` could be pinned at the edge for a day.
class NoStoreOnError
  def initialize(app)
    @app = app
  end

  def call(env)
    status, headers, body = @app.call(env)
    headers = headers.merge("cache-control" => "no-store") if status >= 400
    [status, headers, body]
  end
end
