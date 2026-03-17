# frozen_string_literal: true

require "money/currency"
require "oj"
require "roda"
require "set"

require "providers/ecb"
require "providers/boc"
require "providers/tcmb"
require "versions/v2/query"

module Versions
  class V2 < Roda
    plugin :json,
      content_type: "application/json; charset=utf-8",
      serializer: ->(o) { Oj.dump(o, mode: :compat) }

    plugin :caching
    plugin :indifferent_params
    plugin :halt
    plugin :error_handler do |error|
      status = case error
      when Query::NotFoundError then 404
      when Query::ValidationError then 422
      else 500
      end
      request.halt(status, { message: error.message })
    end

    route do |r|
      response.cache_control(public: true, max_age: 900)

      r.on("rates") do
        r.get do
          query = Query.new(r.params)
          r.etag(query.cache_key)

          query.to_a
        end
      end

      r.is("currencies") do
        r.get do
          currencies
        end
      end

      r.is("providers") do
        r.get do
          providers
        end
      end
    end

    private

    def currencies
      result = {}
      Rate.select(:quote, :provider).distinct.each do |row|
        (result[row.quote] ||= Set.new) << row.provider
      end
      Rate.select(:base, :provider).distinct.each do |row|
        (result[row.base] ||= Set.new) << row.provider
      end

      result.sort.map do |iso, providers|
        currency = Money::Currency.find(iso)
        {
          iso_code: iso,
          iso_numeric: currency&.iso_numeric,
          name: currency&.name || iso,
          symbol: currency&.symbol,
          providers: providers.to_a,
        }
      end
    end

    def providers
      date_ranges = Rate.group(:provider)
        .select { [provider, min(date).as(start_date), max(date).as(end_date)] }
        .to_h { |r| [r[:provider], { start_date: r[:start_date].to_s, end_date: r[:end_date].to_s }] }
      currencies = Rate.select(:provider, :quote).distinct.order(:provider, :quote).all
        .group_by(&:provider).transform_values { |rows| rows.map(&:quote) }

      Providers.all.map(&:new).sort_by(&:key).map do |provider|
        range = date_ranges[provider.key] || {}
        {
          key: provider.key,
          name: provider.name,
          base: provider.base,
          start_date: range[:start_date],
          end_date: range[:end_date],
          currencies: currencies[provider.key] || [],
        }
      end
    end
  end
end
