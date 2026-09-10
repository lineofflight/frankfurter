# frozen_string_literal: true

require "csv"
require "currency"
require "heavy_slots"
require "oj"
require "provider"
require "request_timeout"
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

    DEFAULT_CACHE_CONTROL = "public, max-age=86400, stale-while-revalidate=86400, stale-if-error=86400"

    plugin :json,
           content_type: "application/json; charset=utf-8",
           serializer: ->(o) { Oj.dump(o, mode: :compat) }

    plugin :type_routing,
           types: { csv: "text/csv" }

    plugin :streaming
    plugin :caching
    plugin :indifferent_params
    plugin :halt
    plugin :pass
    plugin :status_handler
    status_handler(404) { { status: 404, message: "not found" } }

    plugin :error_handler do |error|
      status = case error
               when RateQuery::ValidationError then 422
               when RequestTimeout::Error then 503
               when HeavySlots::Busy
                 response["retry-after"] = HeavySlots::RETRY_AFTER_SECONDS.to_s
                 503
               else 500
               end
      request.halt(status, { status:, message: error.message })
    end

    route do |r|
      response["cache-control"] = DEFAULT_CACHE_CONTROL

      r.is { ROOT_PAYLOAD }
      r.root { ROOT_PAYLOAD }

      r.on("rates") do
        r.get { rates_response(r.params) }
      end

      r.on("rate", String, String) do |base_currency, quote_currency|
        r.get { rate_response(r.params, base_currency, quote_currency) }
      end

      # /<key>/rates and /<key>/rate/<base>/<quote> alias /rates?providers=<key> byte for byte (#643): one code path, so
      # single-provider behaviour cannot drift between the two URLs.
      r.on(String) do |key|
        provider = Provider[key.upcase] || r.pass
        if r.params.key?("providers")
          raise RateQuery::ValidationError, "providers is implied by the route; drop the parameter"
        end

        params = r.params.merge("providers" => provider.key)

        r.on("rates") do
          r.get { rates_response(params) }
        end

        r.on("rate", String, String) do |base_currency, quote_currency|
          r.get { rate_response(params, base_currency, quote_currency) }
        end

        r.csv { r.halt(406) }
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

    def rates_response(params)
      query = RateQuery.new(params)
      response["cache-control"] = cache_control_for(query)
      request.etag(query.cache_key)

      request.csv do
        if query.range?
          first, rest = eager_split(query)
          response["Content-Type"] = "text/csv"
          headers = csv_headers(query)
          stream_query(query) do |out|
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

      if ndjson?(request)
        first, rest = eager_split(query)
        response["Vary"] = "Accept"
        response["Content-Type"] = "application/x-ndjson"
        stream_query(query) do |out|
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
        stream_query(query) do |out|
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

    def rate_response(params, base_currency, quote_currency)
      params = params.merge("base" => base_currency.upcase, "quotes" => quote_currency.upcase)
      query = RateQuery.new(params)
      response["cache-control"] = cache_control_for(query)
      query.to_a.first || request.halt(404)
    end

    # Date-relative queries anchor on Date.today, so their responses go stale at UTC midnight even when no new data
    # arrives (and no purge fires) — e.g. forward-dated provider rates entering scope (#541). Cap max-age at the
    # rollover and drop stale-while-revalidate so the first request after midnight revalidates instead of being served
    # yesterday's snapshot.
    def cache_control_for(query)
      return DEFAULT_CACHE_CONTROL unless query.date_relative?

      "public, max-age=#{seconds_to_utc_midnight}, stale-if-error=86400"
    end

    def seconds_to_utc_midnight
      now = Time.now.utc
      (Time.utc(now.year, now.month, now.day) + 86400 - now).ceil
    end

    # Pull the first record before streaming so deterministic data errors raise in the route block (caught by
    # error_handler) instead of mid-stream after response headers — including Cache-Control — have been flushed.
    #
    # `rest` continues draining the same fiber-backed enumerator via #next; iterating the enumerator with
    # #each instead would restart it from the beginning and re-emit the
    # already-consumed first record.
    def eager_split(query)
      enum = query.each
      first = enum.next
      rest = Enumerator.new do |y|
        loop { y << enum.next }
      end
      [first, rest]
    rescue StopIteration
      [nil, [].each]
    end

    # A heavy range holds a compute slot for as long as its enumerator runs. The fiber behind eager_split never runs its
    # ensure once abandoned, so a client that disconnects mid-stream would strand the slot; Roda closes the stream body
    # on every exit (drained, raised, or closed by the server), and the callback returns the slot there (#650).
    def stream_query(query, &)
      stream(callback: -> { query.release_slot }, &)
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
      return value unless value.is_a?(Array)

      value.map { |p| p[:excluded] ? "#{p[:key]}:#{p[:rate]}*" : "#{p[:key]}:#{p[:rate]}" }.join("|")
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
          publish_cadence: provider.publish_cadence,
          publishes_missed: provider.publishes_missed,
          currencies: provider.currency_coverages.map(&:iso_code).sort,
        }
      end
    end
  end
end
