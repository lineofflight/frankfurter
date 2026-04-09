# frozen_string_literal: true

require "digest"
require "roda"
require "rate"
require "weekly_rate"
require "monthly_rate"
require "roundable"
require "blender"
require "money/currency"
require "peg"

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
            chunk = ds.between(chunk_range)
            chunk = chunk.downsample(group) if group && !rollup?
            rows = chunk.all
            normalize_dates!(rows, date_col) if date_col != :date
            rows.group_by { |r| r[:date] }.each do |_, group_rows|
              emit_blended(group_rows, &block)
            end
          end
        else
          emit_blended(raw_dataset.latest(date_scope).all, &block)
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
        ds = raw_dataset
        if date_scope.is_a?(Range)
          ds.where(date: date_scope).max(:date)
        else
          ds.latest(date_scope).max(:date)
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
        blended = Blender.new(rows, base: effective_base).blend

        if base_peg
          blended = blended.map { |r| r.merge(rate: r[:rate] / base_peg.rate, base:) }
        end

        emitted_quotes = Set.new
        blended.each do |r|
          next if quotes && !quotes.include?(r[:quote])

          emitted_quotes << r[:quote]
          yield({ date: r[:date].to_s, base: r[:base], quote: r[:quote], rate: round(r[:rate]) })
        end

        if base_peg && (!quotes || quotes.include?(base_peg.base))
          anchor_date = blended.map { |r| r[:date] }.max
          if anchor_date && !emitted_quotes.include?(base_peg.base)
            emitted_quotes << base_peg.base
            yield({ date: anchor_date.to_s, base:, quote: base_peg.base, rate: round(1.0 / base_peg.rate) })
          end
        end

        expand_pegs(blended, emitted_quotes, &block)
      end

      def expand_pegs(blended, emitted_quotes)
        return if providers

        reference_date = blended.map { |r| r[:date] }.max
        return unless reference_date

        Peg.all.each do |peg|
          next if peg.quote == base
          next if emitted_quotes.include?(peg.quote)
          next if quotes && !quotes.include?(peg.quote)
          next if reference_date < peg.since

          if peg.base == effective_base
            date = reference_date
            rate = peg.rate / (base_peg&.rate || 1.0)
          else
            anchor = blended.find { |r| r[:quote] == peg.base }
            next unless anchor

            date = anchor[:date]
            rate = anchor[:rate] * peg.rate
          end

          yield({ date: date.to_s, base:, quote: peg.quote, rate: round(rate) })
        end
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
