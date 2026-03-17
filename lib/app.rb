# frozen_string_literal: true

require "rack/cors"
require "roda"

require "versions/v1"
require "versions/v2"

class App < Roda
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
      [:all, { "cache-control" => "public, max-age=900" }],
    ]
  plugin :json
  plugin :caching
  plugin :not_found do
    { message: "not found" }
  end

  route do |r|
    r.root do
      response.cache_control(public: true, max_age: 900)

      v1_count = Rate.ecb.latest.count + 1
      latest = Rate.latest
      quotes = latest.select_map(:quote).uniq
      bases = latest.select_map(:base).uniq
      v2_count = (quotes | bases).size

      {
        name: "Frankfurter",
        description: "Currency data API",
        versions: {
          v1: { path: "/v1", currencies: v1_count },
          v2: { path: "/v2", currencies: v2_count },
        },
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
