# frozen_string_literal: true

require "digest"
require "roda"
require "rate"
require "weekly_rate"
require "monthly_rate"
require "roundable"
require "blender"
require "carry_forward"
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
        blended = Blender.new(rows, base: effective_base).blend

        if base_peg
          blended = blended.map { |r| r.merge(rate: r[:rate] / base_peg.rate, base:) }
        end

        output_date = (target_date || blended.map { |r| r[:date] }.max)&.to_s

        records = []
        emitted_quotes = Set.new
        blended.each do |r|
          next if quotes && !quotes.include?(r[:quote])

          emitted_quotes << r[:quote]
          snapped = snap_peg_rate(r[:quote])
          rate = snapped || r[:rate]
          record = { date: output_date, base: r[:base], quote: r[:quote], rate: round(rate) }
          record[:providers] = r[:providers] if expand_providers? && !snapped && r[:providers]
          records << record
        end

        if base_peg && (!quotes || quotes.include?(base_peg.base))
          if output_date && !emitted_quotes.include?(base_peg.base)
            emitted_quotes << base_peg.base
            records << { date: output_date, base:, quote: base_peg.base, rate: round(1.0 / base_peg.rate) }
          end
        end

        records.concat(pegs(blended, emitted_quotes, output_date))
        records.sort_by! { |r| r[:quote] }
        records.each(&block)
      end

      def pegs(blended, emitted_quotes, output_date)
        return [] if providers

        reference_date = blended.map { |r| r[:date] }.max
        return [] unless reference_date

        date_str = output_date || reference_date.to_s

        Peg.all.filter_map do |peg|
          next if peg.quote == base
          next if emitted_quotes.include?(peg.quote)
          next if quotes && !quotes.include?(peg.quote)
          next if reference_date < peg.since

          if peg.base == effective_base
            rate = peg.rate / (base_peg&.rate || 1.0)
          else
            anchor = blended.find { |r| r[:quote] == peg.base }
            next unless anchor

            rate = anchor[:rate] * peg.rate
          end

          { date: date_str, base:, quote: peg.quote, rate: round(rate) }
        end
      end

      def snap_peg_rate(quote)
        return if providers

        peg = Peg.find(quote)
        return unless peg && peg.base == effective_base

        peg.rate / (base_peg ? base_peg.rate : 1.0)
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
