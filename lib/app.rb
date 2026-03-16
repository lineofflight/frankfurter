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
      "/" => "root.json",
      "/favicon.ico" => "favicon.ico",
      "/robots.txt" => "robots.txt",
      "/v1/openapi.json" => "v1/openapi.json",
    },
    header_rules: [
      [:all, { "cache-control" => "public, max-age=900" }],
    ]
  plugin :json
  plugin :not_found do
    { message: "not found" }
  end

  route do |r|
    r.on("v1") do
      r.run(Versions::V1)
    end

    r.on("v2") do
      r.run(Versions::V2)
    end
  end
end
