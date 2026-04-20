# frozen_string_literal: true

require "csv"
require "currency"
require "oj"
require "provider"
require "roda"
require "versions/v2/rate_query"

module Versions
  class V2 < Roda
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

      r.on("rates") do
        r.get do
          query = RateQuery.new(r.params)
          r.etag(query.cache_key)

          r.csv do
            if query.range?
              response["Content-Type"] = "text/csv"
              stream do |out|
                first = true
                query.each do |record|
                  if first
                    out << CSV.generate_line(record.keys)
                    first = false
                  end
                  out << CSV.generate_line(record.values)
                end
              end
            else
              to_csv(query.to_a)
            end
          end

          if ndjson?(r)
            response["Vary"] = "Accept"
            response["Content-Type"] = "application/x-ndjson"
            stream do |out|
              query.each do |record|
                out << Oj.dump(record, mode: :compat)
                out << "\n"
              end
            end
          elsif query.range?
            response["Content-Type"] = "application/json; charset=utf-8"
            stream do |out|
              out << "["
              first = true
              query.each do |record|
                out << "," unless first
                out << Oj.dump(record, mode: :compat)
                first = false
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
          providers
        end
      end
    end

    private

    def ndjson?(request)
      accept = request.env["HTTP_ACCEPT"] || ""
      accept.include?("application/x-ndjson")
    end

    def to_csv(records)
      CSV.generate do |csv|
        csv << records.first.keys unless records.empty?
        records.each { |r| csv << r.values }
      end
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
