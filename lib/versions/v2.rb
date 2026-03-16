# frozen_string_literal: true

require "oj"
require "roda"

require "providers/ecb"
require "providers/boc"
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

      r.is("currencies") do
        r.get do
          currencies
        end
      end

      r.is("sources") do
        r.get do
          sources
        end
      end
    end

    private

    def currencies
      db = Sequel::Model.db
      rows = db.fetch(<<~SQL).all
        SELECT DISTINCT quote, source FROM currencies ORDER BY quote, source
      SQL

      result = {}
      rows.each do |row|
        result[row[:quote]] ||= { sources: [] }
        result[row[:quote]][:sources] << row[:source]
      end

      # Add base currencies from each source
      db.fetch("SELECT DISTINCT base, source FROM currencies").each do |row|
        result[row[:base]] ||= { sources: [] }
        result[row[:base]][:sources] << row[:source] unless result[row[:base]][:sources].include?(row[:source])
      end

      # Add currency names
      require "money/currency"
      result.each do |iso, data|
        currency = Money::Currency.find(iso)
        data[:name] = currency&.name || iso
      end

      result.sort.to_h
    end

    def sources
      Providers.all.sort_by(&:name).to_h do |klass|
        provider = klass.new
        [provider.key, {
          name: provider.name,
          base: provider.base,
        },]
      end
    end

    def build_rate(params, source: nil)
      rate = Rate.new(params, source:)
      request.halt(400, { message: rate.error }) if rate.error
      request.halt(404, { message: "not found" }) if rate.not_found?

      rate
    end
  end
end
