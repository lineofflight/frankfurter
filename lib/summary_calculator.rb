# frozen_string_literal: true

class SummaryCalculator
  class << self
    def calculate(fx_data, breakdown: nil)
      new(fx_data, breakdown:).calculate
    end
  end

  def initialize(fx_data, breakdown: nil)
    @fx_data = fx_data
    @breakdown = breakdown
    @rates = fx_data[:rates] || {}
  end

  def calculate
    sorted_dates = @rates.keys.sort
    return empty_summary if sorted_dates.empty?

    daily_data = calculate_daily_data(sorted_dates)
    totals = calculate_totals(sorted_dates)

    result = {
      base: @fx_data[:base],
      start_date: @fx_data[:start_date],
      end_date: @fx_data[:end_date],
      totals:,
    }

    result[:breakdown] = daily_data if @breakdown == "day"

    result
  end

  private

  def calculate_daily_data(sorted_dates)
    sorted_dates.map.with_index do |date, index|
      rate = extract_rate(@rates[date])
      prev_rate = index.positive? ? extract_rate(@rates[sorted_dates[index - 1]]) : nil
      pct_change = calculate_pct_change(prev_rate, rate)

      {
        date:,
        rate:,
        pct_change:,
      }
    end
  end

  def calculate_totals(sorted_dates)
    start_rate = extract_rate(@rates[sorted_dates.first])
    end_rate = extract_rate(@rates[sorted_dates.last])
    total_pct_change = calculate_pct_change(start_rate, end_rate)

    all_rates = sorted_dates.filter_map { |d| extract_rate(@rates[d]) }
    mean_rate = all_rates.empty? ? nil : (all_rates.sum.to_f / all_rates.size)

    {
      start_rate:,
      end_rate:,
      total_pct_change:,
      mean_rate:,
    }
  end

  def extract_rate(rate_data)
    return if rate_data.nil?

    rate_data.is_a?(Hash) ? rate_data.values.first : rate_data
  end

  def calculate_pct_change(prev_rate, current_rate)
    return if prev_rate.nil? || current_rate.nil?
    return if prev_rate.zero?

    ((current_rate - prev_rate) / prev_rate * 100).round(4)
  end

  def empty_summary
    {
      base: @fx_data[:base] || "EUR",
      start_date: @fx_data[:start_date],
      end_date: @fx_data[:end_date],
      totals: {
        start_rate: nil,
        end_rate: nil,
        total_pct_change: nil,
        mean_rate: nil,
      },
    }
  end
end
