# frozen_string_literal: true

require "base_conversion"
require "consensus"
require "precision"
require "weighted_average"

# Blends exchange rates from multiple providers into a single set. Rebases each provider to a common base,
# filters outliers via cross-provider consensus, then computes a recency-weighted average.
class Blender
  attr_reader :rates, :base

  def initialize(rates, base:)
    @rates = rates.map { |r| r.is_a?(Hash) ? r : r.to_hash }
    @base = base
  end

  def blend
    WeightedAverage.new(consensus.find).calculate
  end

  def precision
    @precision ||= Precision.derive(rates)
  end

  def outliers
    consensus.outliers
  end

  private

  def consensus
    @consensus ||= begin
      rebased_rates = BaseConversion.new(rates, base:).convert
      Consensus.new(rebased_rates)
    end
  end
end
