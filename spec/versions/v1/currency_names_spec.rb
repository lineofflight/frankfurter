# frozen_string_literal: true

require_relative "../../helper"
require "versions/v1/currency_names"

module Versions
  class V1 < Roda
    describe CurrencyNames do
      let(:currency_names) do
        CurrencyNames.new
      end

      it "returns currency codes and names" do
        _(currency_names.formatted["USD"]).must_equal("United States Dollar")
      end

      it "has a cache key" do
        _(currency_names.cache_key).wont_be(:empty?)
      end

      it "omits stored unknown and expired codes from the legacy catalogue" do
        ["ZZZ", "SLL"].each do |quote|
          Rate.dataset.insert(provider: "ECB", date: Fixtures.latest_date, base: "EUR", quote:, mid: 2.0)
        end

        names = currency_names.formatted

        _(names).wont_include("ZZZ")
        _(names).wont_include("SLL")
        _(names["USD"]).must_equal("United States Dollar")
      end
    end
  end
end
