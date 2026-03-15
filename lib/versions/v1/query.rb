# frozen_string_literal: true

module Versions
  class V1 < Roda
    class Query
      class << self
        def build(params)
          query = new(params).to_h
          raise ArgumentError, "bad currency pair" if [query[:base]] == query[:symbols]

          query
        end
      end

      def initialize(params = {})
        @params = params
      end

      def amount
        return unless @params[:amount]

        value = @params[:amount].to_f
        raise ArgumentError, "invalid amount" unless value.positive?

        value
      end

      def base
        @params.values_at(:from, :base).compact.first&.upcase
      end

      def symbols
        @params.values_at(:to, :symbols).compact.first&.upcase&.split(",")
      end

      def date
        if @params[:date]
          Date.parse(@params[:date])
        else
          (Date.parse(@params[:start_date])..Date.parse(@params[:end_date]))
        end
      end

      def to_h
        { amount:, base:, date:, symbols: }.compact
      end
    end
  end
end
