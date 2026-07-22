# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe Adapter do
      it "requires fetch" do
        _ { Class.new(Adapter).new.fetch }.must_raise(NotImplementedError)
      end

      describe ".fetch_each" do
        it "yields records from fetch" do
          fetching_klass = Class.new(Adapter) do
            define_method(:fetch) do |**|
              [{ date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 }]
            end
          end

          batches = []
          fetching_klass.fetch_each { |records| batches << records }

          _(batches.length).must_equal(1)
          _(batches[0].length).must_equal(1)
        end

        it "skips empty results" do
          empty_klass = Class.new(Adapter) do
            define_method(:fetch) do |**|
              []
            end
          end

          batches = []
          empty_klass.fetch_each { |records| batches << records }

          _(batches).must_be_empty
        end

        it "chunks by backfill_range" do
          after = Date.today - 90
          params = []
          chunked_klass = Class.new(Adapter) do
            class << self
              def backfill_range = 30
            end

            define_method(:fetch) do |after: nil, upto: nil|
              params << { after:, upto: }
              [{ date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 }]
            end
          end

          chunked_klass.fetch_each(after:) { |_| }

          _(params.length).must_equal(4)
          _(params[0][:after]).must_equal(after)
          _(params[0][:upto]).must_equal(after + 29)
          _(params[-1][:upto]).must_be_nil
        end
      end

      describe "http client" do
        let(:adapter) { Class.new(Provider::Adapters::Adapter).new }
        let(:client) { adapter.send(:http) }
        let(:url) { "https://example.test/rates" }

        after { WebMock.reset! }

        it "returns 2xx responses" do
          WebMock.stub_request(:get, url).to_return(status: 200, body: "ok")

          assert_equal("ok", client.get(url).to_s)
        end

        it "raises on 4xx" do
          WebMock.stub_request(:get, url).to_return(status: 403, body: "<html>blocked</html>")

          error = assert_raises(HTTP::StatusError) { client.get(url) }
          assert_equal(403, error.response.code)
        end

        it "raises on 5xx" do
          WebMock.stub_request(:get, url).to_return(status: 500)

          assert_raises(HTTP::StatusError) { client.get(url) }
        end

        it "raises on 3xx so moved endpoints fail loudly" do
          WebMock.stub_request(:get, url).to_return(status: 301, headers: { "Location" => "https://example.test/new" })

          assert_raises(HTTP::StatusError) { client.get(url) }
        end

        it "identifies as Frankfurter" do
          stub = WebMock.stub_request(:get, url)
            .with(headers: { "User-Agent" => "Mozilla/5.0 (compatible; Frankfurter; +https://frankfurter.dev)" })
            .to_return(status: 200, body: "ok")

          client.get(url).to_s

          WebMock.assert_requested(stub)
        end
      end
    end
  end
end
