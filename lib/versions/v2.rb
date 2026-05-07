# frozen_string_literal: true

require "csv"
require "currency"
require "oj"
require "provider"
require "roda"
require "versions/v2/rate_query"

module Versions
  class V2 < Roda
    ROOT_PAYLOAD = {
      version: "v2",
      status: "current",
      openapi: "/v2/openapi.json",
      docs: "https://frankfurter.dev",
    }.freeze

    plugin :json,
      content_type: "application/json; charset=utf-8",
      serializer: ->(o) { Oj.dump(o, mode: :compat) }

    plugin :type_routing,
      types: { csv: "text/csv" }

    plugin :streaming
    plugin :caching
    plugin :indifferent_params
    plugin :halt
    plugin :status_handler
    status_handler(404) { { status: 404, message: "not found" } }

    plugin :error_handler do |error|
      status = case error
      when RateQuery::ValidationError then 422
      else 500
      end
      request.halt(status, { status:, message: error.message })
    end

    route do |r|
      response.cache_control(public: true, max_age: 86400)

      r.is { ROOT_PAYLOAD }
      r.root { ROOT_PAYLOAD }

      r.on("rates") do
        r.get do
          query = RateQuery.new(r.params)
          r.etag(query.cache_key)

          r.csv do
            if query.range?
              first, rest = eager_split(query)
              response["Content-Type"] = "text/csv"
              headers = csv_headers(query)
              stream do |out|
                out << CSV.generate_line(headers)
                if first
                  out << CSV.generate_line(headers.map { |k| csv_value(first[k]) })
                  rest.each do |record|
                    out << CSV.generate_line(headers.map { |k| csv_value(record[k]) })
                  end
                end
              end
            else
              to_csv(query.to_a, query)
            end
          end

          if ndjson?(r)
            first, rest = eager_split(query)
            response["Vary"] = "Accept"
            response["Content-Type"] = "application/x-ndjson"
            stream do |out|
              if first
                out << Oj.dump(first, mode: :compat)
                out << "\n"
                rest.each do |record|
                  out << Oj.dump(record, mode: :compat)
                  out << "\n"
                end
              end
            end
          elsif query.range?
            first, rest = eager_split(query)
            response["Content-Type"] = "application/json; charset=utf-8"
            stream do |out|
              out << "["
              if first
                out << Oj.dump(first, mode: :compat)
                rest.each do |record|
                  out << ","
                  out << Oj.dump(record, mode: :compat)
                end
              end
              out << "]"
            end
          else
            query.to_a
          end
        end
      end

      r.on("rate", String, String) do |base_currency, quote_currency|
        r.get do
          params = r.params.merge("base" => base_currency.upcase, "quotes" => quote_currency.upcase)
          query = RateQuery.new(params)
          result = query.to_a.first || r.halt(404)

          result
        end
      end

      r.csv { r.halt(406) }

      r.on("currency", String) do |code|
        r.get do
          found = Currency.find(code)
          found ? found.to_h_with_providers : request.halt(404)
        end
      end

      r.on("currencies") do
        r.get do
          currencies(r.params)
        end
      end

      r.is("providers") do
        r.get do
          response.cache_control(public: true, max_age: 3600)
          providers
        end
      end
    end

    private

    # Pull the first record before streaming so deterministic data errors raise
    # in the route block (caught by error_handler) instead of mid-stream after
    # response headers — including Cache-Control — have been flushed.
    def eager_split(query)
      enum = query.each
      [enum.next, enum]
    rescue StopIteration
      [nil, [].each]
    end

    def ndjson?(request)
      accept = request.env["HTTP_ACCEPT"] || ""
      accept.include?("application/x-ndjson")
    end

    def to_csv(records, query = nil)
      CSV.generate do |csv|
        return csv.string if records.empty?

        headers = query ? csv_headers(query) : records.first.keys
        csv << headers
        records.each { |r| csv << headers.map { |k| csv_value(r[k]) } }
      end
    end

    def csv_headers(query)
      base = [:date, :base, :quote, :rate]
      query.expand_providers? ? base + [:providers] : base
    end

    def csv_value(value)
      value.is_a?(Array) ? value.join("|") : value
    end

    def currencies(params)
      provider_keys = params["providers"]&.upcase&.split(",")
      records = if provider_keys
        Currency.with_providers(provider_keys).all
      elsif params["scope"] == "all"
        Currency.all
      else
        Currency.active
      end

      records.map(&:to_h)
    end

    def providers
      Provider.eager(:currency_coverages).all.sort_by(&:key).filter_map do |provider|
        next if provider.currency_coverages.empty?

        {
          key: provider.key,
          name: provider.name,
          country_code: provider.country_code,
          rate_type: provider.rate_type,
          pivot_currency: provider.pivot_currency,
          data_url: provider.data_url,
          terms_url: provider.terms_url,
          start_date: provider.start_date,
          end_date: provider.end_date,
          publishes_missed: provider.publishes_missed,
          currencies: provider.currency_coverages.map(&:iso_code).sort,
        }
      end
    end
  end
end
