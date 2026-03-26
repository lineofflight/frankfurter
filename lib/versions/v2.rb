# frozen_string_literal: true

require "currency"
require "oj"
require "provider"
require "roda"
require "providers/ecb"
require "providers/boc"
require "providers/tcmb"
require "providers/nbu"
require "providers/cba"
require "providers/nbrb"
require "providers/bob"
require "providers/cbr"
require "providers/nbp"
require "providers/fred"
require "providers/bnm"
require "providers/rba"
require "providers/bcra"
require "providers/cbk"
require "providers/boj"
require "providers/imf"
require "providers/nbrm"
require "providers/bceao"
require "providers/boi"
require "providers/bccr"
require "versions/v2/query"

module Versions
  class V2 < Roda
    plugin :json,
      content_type: "application/json; charset=utf-8",
      serializer: ->(o) { Oj.dump(o, mode: :compat) }

    plugin :caching
    plugin :indifferent_params
    plugin :halt
    plugin :status_handler
    status_handler(404) { { status: 404, message: "not found" } }

    plugin :error_handler do |error|
      status = case error
      when Query::ValidationError then 422
      else 500
      end
      request.halt(status, { status:, message: error.message })
    end

    route do |r|
      response.cache_control(public: true, max_age: 86400)

      r.on("rates") do
        r.get do
          query = Query.new(r.params)
          r.etag(query.cache_key)

          query.to_a
        end
      end

      r.on("rate", String, String) do |base_currency, quote_currency|
        r.get(String) do |date_str|
          rate_response(base_currency, quote_currency, date: date_str)
        end

        r.get do
          rate_response(base_currency, quote_currency)
        end
      end

      r.on("currencies") do
        r.get(String) do |code|
          found = Currency.find(code)
          found ? found.to_h_with_providers : request.halt(404)
        end

        r.get do
          ds = r.params["scope"] == "all" ? Currency : Currency.active
          ds.map(&:to_h)
        end
      end

      r.is("providers") do
        r.get do
          providers
        end
      end
    end

    private

    def rate_response(base_currency, quote_currency, date: nil)
      params = {
        base: base_currency.upcase,
        quotes: quote_currency.upcase,
        date: date,
        providers: request.params["providers"],
      }.compact
      query = Query.new(params)
      result = query.to_a.first
      result || request.halt(404)
    end

    def providers
      date_ranges = Rate.group(:provider)
        .select { [provider, min(date).as(start_date), max(date).as(end_date)] }
        .to_h { |r| [r[:provider], { start_date: r[:start_date].to_s, end_date: r[:end_date].to_s }] }
      currencies = Rate.select(:provider, Sequel[:quote].as(:currency)).distinct
        .union(Rate.select(:provider, Sequel[:base].as(:currency)).distinct)
        .order(:provider, :currency).all
        .group_by(&:provider).transform_values { |rows| rows.map { |r| r[:currency] }.uniq.sort }

      Provider.all.sort_by(&:key).map do |provider|
        range = date_ranges[provider.key] || {}
        {
          key: provider.key,
          name: provider.name,
          description: provider.description,
          data_url: provider.data_url,
          terms_url: provider.terms_url,
          start_date: range[:start_date],
          end_date: range[:end_date],
          currencies: currencies[provider.key] || [],
        }
      end
    end
  end
end
