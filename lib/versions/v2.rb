# frozen_string_literal: true

require "digest"
require "oj"
require "roda"

require "currency_names"
require "query"
require "quote"
require "source"

module Versions
  class V2 < Roda
    plugin :json,
      content_type: "application/json; charset=utf-8",
      serializer: ->(o) { Oj.dump(o, mode: :compat) }

    plugin :caching
    plugin :indifferent_params
    plugin :params_capturing
    plugin :halt

    route do |r|
      response.cache_control(public: true, max_age: 900)

      # GET /v2/latest
      r.is(/latest|current/) do
        r.params["date"] = Date.today.to_s
        quote = quote_end_of_day(r)
        r.etag(quote.cache_key)

        quote.formatted
      end

      # GET /v2/YYYY-MM-DD
      r.is(/(\d{4}-\d{2}-\d{2})/) do
        r.params["date"] = r.params["captures"].first
        quote = quote_end_of_day(r)
        r.etag(quote.cache_key)

        quote.formatted
      end

      # GET /v2/YYYY-MM-DD..YYYY-MM-DD
      r.is(/(\d{4}-\d{2}-\d{2})\.\.(\d{4}-\d{2}-\d{2})?/) do
        r.params["start_date"] = r.params["captures"].first
        r.params["end_date"] = r.params["captures"][1] || Date.today.to_s
        quote = quote_interval(r)
        r.etag(quote.cache_key)

        quote.formatted
      end

      # GET /v2/sources
      r.is("sources") do
        sources = Source.all.map do |s|
          {
            code: s.code,
            name: s.name,
            base_currency: s.base_currency,
          }
        end

        cache_key = Digest::MD5.hexdigest(sources.to_s)
        r.etag(cache_key)

        { sources: sources }
      end

      # GET /v2/currencies
      r.is("currencies") do
        currency_names = CurrencyNames.new
        r.etag(currency_names.cache_key)

        currency_names.formatted
      end
    end

    private

    def quote_end_of_day(request)
      # V2 always includes source key to enable strict source resolution
      params_with_source = request.params.merge(source: request.params[:source])
      query = Query.build(params_with_source)
      quote = Quote::EndOfDay.new(**query)
      quote.perform
      request.halt(404, { message: "not found" }) if quote.not_found?

      quote
    rescue ArgumentError => e
      request.halt(400, { error: e.message })
    end

    def quote_interval(request)
      # V2 always includes source key to enable strict source resolution
      params_with_source = request.params.merge(source: request.params[:source])
      query = Query.build(params_with_source)
      quote = Quote::Interval.new(**query)
      quote.perform
      request.halt(404, { message: "not found" }) if quote.not_found?

      quote
    rescue ArgumentError => e
      request.halt(400, { error: e.message })
    end
  end
end
