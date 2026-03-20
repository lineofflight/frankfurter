# frozen_string_literal: true

require "digest"
require "rate"
require "roundable"
require "blender"

module Versions
  class V2 < Roda
    class Query
      include Roundable

      class ValidationError < StandardError; end

      def initialize(params)
        @params = params
        validate!
      end

      def to_a
        @rates ||= fetch_rates
      end

      def cache_key
        Digest::MD5.hexdigest(to_a.last&.dig(:date).to_s)
      end

      private

      def base
        @params[:base]&.upcase || "EUR"
      end

      def quotes
        @params[:quotes]&.upcase&.split(",")
      end

      def providers
        @params[:providers]&.upcase&.split(",")
      end

      def group
        @params[:group]&.downcase
      end

      def date
        parse_date(@params[:date])
      end

      def start_date
        parse_date(@params[:from])
      end

      def end_date
        parse_date(@params[:to])
      end

      def parse_date(value)
        return unless value

        Date.parse(value)
      rescue Date::Error
        nil
      end

      def validate!
        validate_dates!
        validate_conflicting_params!
        validate_group!
        validate_currencies!
      end

      def validate_dates!
        raise ValidationError, "invalid date" if [:date, :from, :to].any? { |key| @params[key] && !parse_date(@params[key]) }
      end

      def validate_conflicting_params!
        raise ValidationError, "conflicting params" if date && (start_date || end_date)
      end

      def validate_group!
        raise ValidationError, "invalid group" if group && !["week", "month"].include?(group)
      end

      def validate_currencies!
        invalid = []
        invalid << base if @params[:base] && !Money::Currency.find(base)
        invalid.concat(quotes.reject { |q| Money::Currency.find(q) }) if quotes
        raise ValidationError, "invalid currency: #{invalid.join(",")}" if invalid.any?
      end

      def date_scope
        if date
          date
        elsif start_date
          start_date..(end_date || Date.today)
        else
          Date.today
        end
      end

      def fetch_rates
        ds = Rate.dataset
        ds = ds.where(provider: providers) if providers

        rates = if date_scope.is_a?(Range)
          ds = ds.where(date: date_scope)
          ds = ds.downsample(group) if group
          ds.all.group_by { |r| r[:date] }.flat_map do |_, rows|
            Blender.new(rows, base:).blend
          end
        else
          Blender.new(ds.latest(date_scope).all, base:).blend
        end

        rates.filter_map do |r|
          next if quotes && !quotes.include?(r[:quote])

          { date: r[:date].to_s, base: r[:base], quote: r[:quote], rate: round(r[:rate]) }
        end
      end
    end
  end
end
