# frozen_string_literal: true

require "oj"
require "roda"

require "versions/v2/rate"

module Versions
  class V2 < Roda
    plugin :json,
      content_type: "application/json; charset=utf-8",
      serializer: ->(o) { Oj.dump(o, mode: :compat) }

    plugin :caching
    plugin :indifferent_params
    plugin :halt
    plugin :error_handler do |error|
      request.halt(422, { message: error.message })
    end

    route do |r|
      response.cache_control(public: true, max_age: 900)

      r.on(String, "rates") do |source|
        r.get do
          rate = build_rate(r.params, source:)
          r.etag(rate.cache_key)

          rate.formatted
        end
      end

      r.on("rates") do
        r.get do
          rate = build_rate(r.params)
          r.etag(rate.cache_key)

          rate.formatted
        end
      end
    end

    private

    def build_rate(params, source: nil)
      rate = Rate.new(params, source:)
      request.halt(400, { message: rate.error }) if rate.error
      request.halt(404, { message: "not found" }) if rate.not_found?

      rate
    end
  end
end
