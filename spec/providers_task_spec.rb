# frozen_string_literal: true

require_relative "helper"
require "rake"
load File.expand_path("../lib/tasks/providers.rake", __dir__)

describe "Backfill task" do
  def invoke_backfill(*)
    Rake::Task[:backfill].reenable
    Cache.stub(:purge_pending, nil) { Rake::Task[:backfill].invoke(*) }
  end

  def with_full_history
    previous = ENV["FULL"]
    ENV["FULL"] = "1"
    yield
  ensure
    previous ? ENV["FULL"] = previous : ENV.delete("FULL")
  end

  it "starts a named provider at coverage_start when full history is requested" do
    provider = Provider["ECB"].dup
    calls = []
    provider.stub(:backfill, ->(**options) { calls << options }) do
      Provider.stub(:detect, ->(*) { provider }) { with_full_history { invoke_backfill("ecb") } }
    end

    _(calls).must_equal([{ after: provider.coverage_start }])
  end

  it "starts every provider at its own coverage_start when full history is requested" do
    providers = [Provider["ECB"].dup, Provider["BOC"].dup]
    calls = Queue.new
    providers.first.stub(:backfill, ->(**options) { calls << ["ECB", options] }) do
      providers.last.stub(:backfill, ->(**options) { calls << ["BOC", options] }) do
        Provider.stub(:to_a, providers) do
          with_full_history { invoke_backfill }
        end
      end
    end

    results = Array.new(calls.size) { calls.pop }.to_h

    _(results).must_equal(providers.to_h { |provider| [provider.key, { after: provider.coverage_start }] })
  end

  it "keeps the incremental default" do
    provider = Provider["ECB"].dup
    calls = []
    provider.stub(:backfill, ->(**options) { calls << options }) do
      Provider.stub(:detect, ->(*) { provider }) { invoke_backfill("ecb") }
    end

    _(calls).must_equal([{}])
  end
end
