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

      ALLOWED_EXPANSIONS = ["providers"].freeze
      ALLOWED_PARAMS = ["base", "quotes", "providers", "date", "from", "to", "group", "expand"].freeze
      CHUNK_MONTHS = { "week" => 21, "month" => 84 }.freeze
      DEFAULT_CHUNK_MONTHS = 3
      PIVOT = "USD"

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

            rows = ds.between(chunk_range).all
            normalize_dates!(rows, date_col) if date_col != :date
            rows.group_by { |r| r[:date] }.each do |_, group_rows|
              emit_blended(group_rows, &block)
            end
          end
        else
          window = raw_dataset.where(date: (date_scope - CarryForward::LOOKBACK_DAYS)..date_scope)
          rows = CarryForward.apply(window.naked.all, date: date_scope)
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
          ds.where(date: (date_scope - CarryForward::LOOKBACK_DAYS)..date_scope).max(:date)
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
          currencies << PIVOT
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

      def emit_blended(rows, &block)
        blended = if fast_path?(rows)
          fast_path_blend(rows)
        else
          pivot_path_blend(rows)
        end

        return if blended.empty?

        records = blended.filter_map do |r|
          next if quotes && !quotes.include?(r[:quote])

          record = { date: r[:date].to_s, base: r[:base], quote: r[:quote], rate: round(r[:rate]) }
          record[:providers] = r[:providers] if expand_providers? && r[:providers]
          record
        end

        records.sort_by! { |r| r[:quote] }
        records.each(&block)
      end

      def fast_path_blend(rows)
        blended = Blender.new(rows, base: effective_base).blend
        blended = PegAnchor.apply(blended, base: effective_base) unless providers
        scale_for_pegged_base(blended)
      end

      def pivot_path_blend(rows)
        blended = Blender.new(rows, base: PIVOT).blend
        blended = PegAnchor.apply(blended, base: PIVOT) unless providers
        return [] if blended.empty?
        return blended if base == PIVOT

        derive(blended, target: base)
      end

      # Fast-path counterpart to derive: when the request base is pegged but every input row already lives in the
      # peg's base (effective_base), the blend stays in effective_base. We then scale the rows to the user's base by
      # 1/peg.rate and append a base->peg.base row. Mirrors the legacy PegAnchor#scale_to_user_base + base_peg_row,
      # confined to the fast path; the pivot path's `derive` already yields rows in the requested base.
      def scale_for_pegged_base(rows)
        return rows unless base_peg
        return [] if rows.empty?

        scaled = rows.map { |r| r.merge(rate: r[:rate] / base_peg.rate, base: base) }
        unless scaled.any? { |r| r[:quote] == base_peg.base }
          ref = scaled.map { |r| r[:date] }.max
          scaled << { date: ref, base: base, quote: base_peg.base, rate: 1.0 / base_peg.rate }
        end
        scaled
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

      # True iff every input row already has effective_base as its :base. In that case there is nothing to
      # cross-convert, so the blend can run directly in user's base without round-tripping through PIVOT.
      def fast_path?(rows)
        rows.all? { |r| r[:base] == effective_base }
      end

      # Given rows blended in PIVOT (one per :quote), return rows rebased to `target` by division. Appends a
      # `target -> PIVOT` row. Rows whose quote is `target` are dropped (no base->base row). Returns [] if
      # `target` is not present in the input (no path to derive).
      def derive(rows, target:)
        return [] if rows.empty?

        pivot_to_target = rows.find { |r| r[:quote] == target }
        return [] unless pivot_to_target

        derived = rows.filter_map do |r|
          next if r[:quote] == target

          r.merge(base: target, rate: r[:rate] / pivot_to_target[:rate])
        end

        derived << pivot_to_target.merge(
          base: target,
          quote: pivot_to_target[:base],
          rate: 1.0 / pivot_to_target[:rate],
        )
        derived
      end
    end
  end
end
