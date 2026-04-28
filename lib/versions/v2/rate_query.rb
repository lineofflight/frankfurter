# frozen_string_literal: true

require "digest"
require "set"

require "roda"
require "rate"
require "weekly_rate"
require "monthly_rate"
require "roundable"
require "blender"
require "carry_forward"
require "money/currency"
require "peg"
require "peg_anchor"

module Versions
  class V2 < Roda
    class RateQuery
      include Roundable

      class ValidationError < StandardError; end

      CHUNK_MONTHS = { "week" => 21, "month" => 84 }.freeze
      DEFAULT_CHUNK_MONTHS = 3

      def initialize(params)
        @params = params
        validate!
      end

      def to_a
        @rates ||= [].tap { |a| each { |r| a << r } }
      end

      def each(&block)
        return to_enum(:each) unless block

        if date_scope.is_a?(Range)
          each_chunk(date_scope) do |chunk_range|
            ds = range_dataset
            date_col = ds.model.date_column

            if rollup?
              rows = ds.between(chunk_range).all
              normalize_dates!(rows, date_col) if date_col != :date
              rows.group_by { |r| r[:date] }.each do |_, group_rows|
                emit_blended(group_rows, &block)
              end
            else
              expanded = (chunk_range.begin - CarryForward::RANGE_LOOKBACK_DAYS)..chunk_range.end
              rows = ds.between(expanded).naked.all
              CarryForward.enrich(rows, range: chunk_range).each do |target_date, group_rows|
                emit_blended(group_rows, target_date:, &block)
              end
            end
          end
        else
          window = raw_dataset.where(date: (date_scope - CarryForward::LATEST_LOOKBACK_DAYS)..date_scope)
          rows = CarryForward.latest(window.naked.all, date: date_scope)
          emit_blended(rows, &block)
        end
      end

      def range?
        date_scope.is_a?(Range)
      end

      def cache_key
        Digest::MD5.hexdigest([max_date, expand].join("|"))
      end

      def expand_providers?
        expand&.include?("providers") || false
      end

      private

      def max_date
        ds = raw_dataset
        if date_scope.is_a?(Range)
          ds.where(date: date_scope).max(:date)
        else
          ds.where(date: (date_scope - CarryForward::LATEST_LOOKBACK_DAYS)..date_scope).max(:date)
        end
      end

      def rollup?
        range? && ["week", "month"].include?(group)
      end

      def rollup_model
        case group
        when "week" then WeeklyRate
        when "month" then MonthlyRate
        end
      end

      def apply_filters(ds)
        ds = ds.where(provider: providers) if providers
        if quotes
          currencies = Set.new(quotes)
          currencies << effective_base
          quotes.each { |q| (peg = Peg.find(q)) && currencies << peg.base }
          ds = ds.only(*currencies)
        end
        ds
      end

      def raw_dataset
        apply_filters(Rate.dataset)
      end

      def range_dataset
        apply_filters(rollup? ? rollup_model.dataset : Rate.dataset)
      end

      def base
        @params[:base]&.upcase || "EUR"
      end

      def base_peg
        return @base_peg if defined?(@base_peg)

        @base_peg = Peg.find(base)
      end

      def effective_base
        base_peg&.base || base
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

      def expand
        @params[:expand]&.downcase&.split(",")
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

      ALLOWED_PARAMS = ["base", "quotes", "providers", "date", "from", "to", "group", "expand"].freeze
      ALLOWED_EXPANSIONS = ["providers"].freeze

      def validate!
        validate_params!
        validate_dates!
        validate_conflicting_params!
        validate_group!
        validate_expand!
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

      def validate_expand!
        return unless expand

        unknown = expand - ALLOWED_EXPANSIONS
        raise ValidationError, "invalid expand: #{unknown.join(",")}" if unknown.any?
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

      def emit_blended(rows, target_date: nil, &block)
        blended = if providers
          Blender.new(rows, base: base).blend
        else
          PegAnchor.new(rows, base: base).blend
        end
        return if blended.empty?

        output_date = (target_date || blended.map { |r| r[:date] }.max)&.to_s

        records = blended.filter_map do |r|
          next if quotes && !quotes.include?(r[:quote])

          record = { date: output_date, base: r[:base], quote: r[:quote], rate: round(r[:rate]) }
          record[:providers] = r[:providers] if expand_providers? && r[:providers]
          record
        end

        records.sort_by! { |r| r[:quote] }
        records.each(&block)
      end

      def normalize_dates!(rows, date_col)
        rows.each { |r| r.values[:date] = r.values.delete(date_col) }
      end

      def each_chunk(range)
        months = CHUNK_MONTHS.fetch(group, DEFAULT_CHUNK_MONTHS)
        cursor = range.begin
        while cursor <= range.end
          chunk_end = [(cursor >> months) - 1, range.end].min
          yield cursor..chunk_end
          cursor = chunk_end + 1
        end
      end
    end
  end
end
