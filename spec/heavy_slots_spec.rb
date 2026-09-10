# frozen_string_literal: true

require_relative "helper"
require "heavy_slots"

describe HeavySlots do
  it "hands out slots up to the cap and refuses past it" do
    slots = HeavySlots.new(2)

    _(slots.try_acquire).must_equal(true)
    _(slots.try_acquire).must_equal(true)
    _(slots.try_acquire).must_equal(false)
    _(slots.held).must_equal(2)
  end

  it "frees a slot on release" do
    slots = HeavySlots.new(1)
    slots.try_acquire
    slots.release

    _(slots.held).must_equal(0)
    _(slots.try_acquire).must_equal(true)
  end

  it "never counts below zero" do
    slots = HeavySlots.new(1)
    slots.release

    _(slots.held).must_equal(0)
  end

  it "reads the cap from the environment" do
    _(HeavySlots::DEFAULT_MAX).must_equal(Integer(ENV.fetch("MAX_HEAVY_COMPUTES", 2)))
    _(HeavySlots.new.max).must_equal(HeavySlots::DEFAULT_MAX)
  end
end
