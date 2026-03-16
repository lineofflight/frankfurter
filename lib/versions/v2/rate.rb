# frozen_string_literal: true

require "digest"
require "currency"

module Versions
  class V2 < Roda
    class Rate
      attr_reader :error

      def initialize(params, source: nil)
        @base = params[:base]&.upcase || "EUR"
        @symbols = params[:symbols]&.upcase&.split(",")
        @source = source&.upcase
        @date = parse_date(params[:date])
        @start_date = parse_date(params[:from])
        @end_date = parse_date(params[:to])
        @error = validate
      end

      def formatted
        {
          base: @base,
          rates: query_rates,
        }
      end

      def not_found?
        query_rates.empty?
      end

      def cache_key
        return if not_found?

        Digest::MD5.hexdigest(query_rates.last[:date].to_s)
      end

      private

      def parse_date(value)
        return unless value

        Date.parse(value)
      rescue Date::Error
        nil
      end

      def validate
        return "conflicting params" if @date && (@start_date || @end_date)

        nil
      end

      def query_rates
        @query_rates ||= fetch_rates
      end

      def fetch_rates
        results = Sequel::Model.db.fetch(sql, **bind_params).all

        results
          .group_by { |r| r[:date] }
          .sort
          .map do |date, rows|
            rates = rows.to_h { |r| [r[:quote], round(r[:rate])] }
            rates = rates.slice(*@symbols) if @symbols

            { date: date.to_s }.merge(rates)
          end
      end

      def sql
        <<~SQL
          SELECT
            c.date,
            c.quote,
            AVG(c.rate / COALESCE(base_rate.rate, 1.0)) AS rate
          FROM currencies c
          LEFT JOIN currencies base_rate
            ON base_rate.date = c.date
            AND base_rate.source = c.source
            AND base_rate.quote = :base
          WHERE #{date_clause}
            AND c.quote != :base
            AND (base_rate.rate IS NOT NULL OR c.base = :base)
            #{source_clause}
          GROUP BY c.date, c.quote

          UNION ALL

          SELECT
            c.date,
            c.base AS quote,
            AVG(1.0 / c.rate) AS rate
          FROM currencies c
          WHERE #{date_clause}
            AND c.quote = :base
            AND c.base != :base
            #{source_clause}
          GROUP BY c.date, c.base

          ORDER BY 1, 2
        SQL
      end

      def date_clause
        if @date
          "c.date = :date"
        elsif @start_date
          "c.date >= :start_date AND c.date <= :end_date"
        else
          "c.date = (SELECT date FROM currencies ORDER BY date DESC LIMIT 1)"
        end
      end

      def source_clause
        @source ? "AND c.source = :source" : ""
      end

      def round(value)
        if value > 5000
          value.round
        elsif value > 80
          Float(format("%<value>.2f", value:))
        elsif value > 20
          Float(format("%<value>.3f", value:))
        elsif value > 1
          Float(format("%<value>.4f", value:))
        elsif value > 0.0001
          Float(format("%<value>.5f", value:))
        else
          Float(format("%<value>.6f", value:))
        end
      end

      def bind_params
        params = { base: @base }
        params[:date] = @date.to_s if @date
        params[:start_date] = @start_date.to_s if @start_date
        params[:end_date] = (@end_date || Date.today).to_s if @start_date
        params[:source] = @source if @source

        params
      end
    end
  end
end
