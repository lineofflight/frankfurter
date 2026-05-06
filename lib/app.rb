# frozen_string_literal: true

require "rack/cors"
require "roda"

require "no_store_on_error"
require "versions/v1"
require "versions/v2"

class App < Roda
  use NoStoreOnError
  use Rack::Cors do
    allow do
      origins "*"
      resource "*", headers: :any, methods: [:get, :options]
    end
  end

  opts[:root] = File.expand_path("..", __FILE__)
  plugin :static,
    {
      "/favicon.ico" => "favicon.ico",
      "/robots.txt" => "robots.txt",
      "/v1/openapi.json" => "v1/openapi.json",
      "/v2/openapi.json" => "v2/openapi.json",
    },
    header_rules: [
      [:all, { "cache-control" => "public, max-age=86400" }],
    ]
  plugin :json
  plugin :caching
  plugin :not_found do
    { status: 404, message: "not found" }
  end

  route do |r|
    r.root do
      response.cache_control(public: true, max_age: 86400)

      {
        name: "Frankfurter",
        docs: "https://frankfurter.dev",
        source: "https://github.com/lineofflight/frankfurter",
      }
    end

    r.on("v1") do
      r.run(Versions::V1)
    end

    r.on("v2") do
      r.run(Versions::V2)
    end
  end
end
