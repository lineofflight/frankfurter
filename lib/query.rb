# frozen_string_literal: true

# FIXME: Validations are all over. Clean up!
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

  def source
    @params[:source]&.upcase
  end

  def from
    base
  end

  def to
    symbols
  end

  def to_h
    result = { amount:, base:, date:, symbols: }.compact
    result[:source] = source if @params.key?(:source)
    result
  end
end
