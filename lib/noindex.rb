# frozen_string_literal: true

# Rack middleware that tells search engines not to index API responses. Google had crawled hundreds of rate URLs it then
# declined to index; the header makes that explicit. It says nothing about crawling, so user-driven fetchers
# (Claude-User, ChatGPT-User, Perplexity-User) and plain HTTP clients are unaffected, unlike a robots.txt Disallow,
# which some of them honour. The OpenAPI specs stay indexable as the machine-readable entry point.
class Noindex
  EXEMPT = ["/v1/openapi.json", "/v2/openapi.json"].freeze

  def initialize(app)
    @app = app
  end

  def call(env)
    exempt = EXEMPT.include?(env["PATH_INFO"]) # read before the call: Rack::Static rewrites PATH_INFO in place
    status, headers, body = @app.call(env)
    headers = headers.merge("x-robots-tag" => "noindex") unless exempt
    [status, headers, body]
  end
end
