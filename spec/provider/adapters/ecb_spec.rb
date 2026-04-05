# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/ecb"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe ECB do
      let(:adapter) { ECB.new }

      it "fetches rates" do
        dataset = VCR.use_cassette("ecb_historical", match_requests_on: [:method, :host]) do
          adapter.fetch(after: Date.new(2025, 1, 1))
        end

        _(dataset).wont_be_empty
      end
    end
  end
end
