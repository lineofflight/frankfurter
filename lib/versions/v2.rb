# frozen_string_literal: true

require "oj"
require "roda"

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
      db = Sequel::Model.db
      rows = db.fetch(<<~SQL).all
        SELECT DISTINCT quote, provider FROM rates ORDER BY quote, provider
      SQL

      result = {}
      rows.each do |row|
        result[row[:quote]] ||= { providers: [] }
        result[row[:quote]][:providers] << row[:provider]
      end

      # Add base currencies from each provider
      db.fetch("SELECT DISTINCT base, provider FROM rates").each do |row|
        result[row[:base]] ||= { providers: [] }
        result[row[:base]][:providers] << row[:provider] unless result[row[:base]][:providers].include?(row[:provider])
      end

      # Add currency names
      require "money/currency"
      result.each do |iso, data|
        currency = Money::Currency.find(iso)
        data[:name] = currency&.name || iso
      end

      result.sort.to_h
    end

    def providers
      Providers.all.map(&:new).sort_by(&:key).to_h do |provider|
        [provider.key, {
          name: provider.name,
          base: provider.base,
        },]
      end
    end
  end
end
