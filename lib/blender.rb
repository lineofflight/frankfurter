# frozen_string_literal: true

require "base_converter"

# Blends exchange rates from multiple providers into a single set by converting each provider's rates to a common
# base currency and averaging rates for currencies that appear in more than one provider.
class Blender
  attr_reader :rates, :base

  def initialize(rates, base:)
    @rates = rates
    @base = base
  end

  def blend
    rebased = rates.group_by { |r| [r[:date], r[:provider], r[:base]] }.flat_map do |_, provider_rows|
      BaseConverter.new(provider_rows, base: base).convert
    end

    rebased.group_by { |r| [r[:date], r[:quote]] }.sort.map do |_, group|
      group.first.merge(rate: group.sum { |r| r[:rate] } / group.size)
    end
  end
end
