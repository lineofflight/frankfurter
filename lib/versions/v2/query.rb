# frozen_string_literal: true

require "digest"
require "rate"
require "roundable"
require "blender"
require "peg"

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
        @rates ||= [].tap { |a| each { |r| a << r } }
      end

      def each(&block)
        return to_enum(:each) unless block

        ds = Rate.dataset
        ds = ds.where(provider: providers) if providers

        if date_scope.is_a?(Range)
          each_quarter(date_scope) do |chunk_range|
            chunk_ds = ds.where(date: chunk_range)
            chunk_ds = chunk_ds.downsample(group) if group
            chunk_ds.order(:date, :quote).all.group_by { |r| r[:date] }.each do |_, rows|
              emit_blended(rows, &block)
            end
          end
        else
          emit_blended(ds.latest(date_scope).all, &block)
        end
      end

      def range?
        date_scope.is_a?(Range)
      end

      def cache_key
        Digest::MD5.hexdigest(max_date.to_s)
      end

      private

      def max_date
        if date_scope.is_a?(Range)
          ds = Rate.dataset
          ds = ds.where(provider: providers) if providers
          ds.where(date: date_scope).max(:date)
        else
          to_a.map { |r| r[:date] }.max
        end
      end

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

      ALLOWED_PARAMS = ["base", "quotes", "providers", "date", "from", "to", "group"].freeze

      def validate!
        validate_params!
        validate_dates!
        validate_conflicting_params!
        validate_group!
        validate_currencies!
      end

      def validate_params!
        unknown = @params.keys.map(&:to_s) - ALLOWED_PARAMS
        raise ValidationError, "unknown parameter: #{unknown.join(", ")}" if unknown.any?
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

      def emit_blended(rows, &block)
        blended = Blender.new(rows, base:).blend

        emitted_quotes = Set.new
        blended.each do |r|
          next if quotes && !quotes.include?(r[:quote])

          emitted_quotes << r[:quote]
          yield({ date: r[:date].to_s, base: r[:base], quote: r[:quote], rate: round(r[:rate]) })
        end

        expand_pegs(blended, emitted_quotes, &block)
      end

      def expand_pegs(blended, emitted_quotes)
        return if providers

        Peg.all.each do |peg|
          next if emitted_quotes.include?(peg.quote)
          next if quotes && !quotes.include?(peg.quote)

          date = blended.first&.dig(:date)
          next unless date
          next if date < peg.since

          if peg.base == base
            rate = peg.rate
          else
            anchor = blended.find { |r| r[:quote] == peg.base }
            next unless anchor

            rate = anchor[:rate] * peg.rate
          end

          yield({ date: date.to_s, base:, quote: peg.quote, rate: round(rate) })
        end
      end

      def each_quarter(range)
        cursor = range.begin
        while cursor <= range.end
          quarter_end = [(cursor >> 3) - 1, range.end].min
          yield cursor..quarter_end
          cursor = quarter_end + 1
        end
      end
    end
  end
end
