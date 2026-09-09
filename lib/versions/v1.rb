# frozen_string_literal: true

require "oj"
require "roda"

require "versions/v1/currency_names"
require "versions/v1/query"
require "versions/v1/quote/end_of_day"
require "versions/v1/quote/interval"

module Versions
  class V1 < Roda
    ROOT_PAYLOAD = {
      version: "v1",
      status: "deprecated",
      openapi: "/v1/openapi.json",
      docs: "https://frankfurter.dev/v1/",
    }.freeze

    # Wrap the full app so static files, preflight requests, and errors also carry the headers.
    class Deprecation
      def initialize(app)
        @app = app
      end

      def call(env)
        path = env["PATH_INFO"]
        return @app.call(env) unless path == "/v1" || path.start_with?("/v1/")

        successor = case path
                    when "/v1", "/v1/" then "/v2"
                    when "/v1/currencies" then "/v2/currencies"
                    when "/v1/openapi.json" then "/v2/openapi.json"
                    else "/v2/rates"
                    end

        status, headers, body = @app.call(env)
        link = %(<https://api.frankfurter.dev#{successor}>; rel="successor-version")
        headers = headers.merge(
          "deprecation" => "@1779103800", # V2 release: 2026-05-18 11:30 UTC (RFC 9745 structured date).
          "link" => [headers["link"], link].compact.join(", "),
        )

        [status, headers, body]
      end
    end

    plugin :json,
           content_type: "application/json; charset=utf-8",
           serializer: ->(o) { Oj.dump(o, mode: :compat) }

    plugin :caching
    plugin :indifferent_params
    plugin :params_capturing
    plugin :halt
    plugin :error_handler do |error|
      request.halt(422, { message: error.message })
    end

    route do |r|
      response.cache_control(public: true, max_age: 86400)

      r.is { ROOT_PAYLOAD }
      r.root { ROOT_PAYLOAD }

      r.is(/latest|current/) do
        r.params["date"] = Date.today.to_s
        quote = quote_end_of_day(r)
        r.etag(quote.cache_key)

        quote.formatted
      end

      r.is(/(\d{4}-\d{2}-\d{2})/) do
        r.params["date"] = r.params["captures"].first
        quote = quote_end_of_day(r)
        r.etag(quote.cache_key)

        quote.formatted
      end

      r.is(/(\d{4}-\d{2}-\d{2})\.\.(\d{4}-\d{2}-\d{2})?/) do
        r.params["start_date"] = r.params["captures"].first
        r.params["end_date"] = r.params["captures"][1] || Date.today.to_s
        quote = quote_interval(r)
        r.etag(quote.cache_key)

        quote.formatted
      end

      r.is("currencies") do
        currency_names = CurrencyNames.new
        r.etag(currency_names.cache_key)

        currency_names.formatted
      end
    end

    private

    def quote_end_of_day(request)
      query = Query.build(request.params)
      quote = Quote::EndOfDay.new(**query)
      quote.perform
      request.halt(404, { message: "not found" }) if quote.not_found?

      quote
    end

    def quote_interval(request)
      query = Query.build(request.params)
      quote = Quote::Interval.new(**query)
      quote.perform
      request.halt(404, { message: "not found" }) if quote.not_found?

      quote
    end
  end
end
