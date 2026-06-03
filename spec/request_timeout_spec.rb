# frozen_string_literal: true

require_relative "helper"
require "request_timeout"

describe RequestTimeout do
  # An absolute monotonic-clock reading, offset by seconds.
  def clock(offset = 0)
    Process.clock_gettime(Process::CLOCK_MONOTONIC) + offset
  end

  describe RequestTimeout::TimedBody do
    it "yields every chunk when the deadline has not passed" do
      body = RequestTimeout::TimedBody.new(["a", "b", "c"], deadline: clock(1000), seconds: 1000)

      collected = []
      body.each { |chunk| collected << chunk }

      _(collected).must_equal(["a", "b", "c"])
    end

    it "raises once the deadline passes mid-iteration" do
      body = RequestTimeout::TimedBody.new(["a", "b", "c"], deadline: clock(-1), seconds: 5)

      _ { body.each { |_| } }.must_raise(RequestTimeout::Error)
    end

    it "delegates close to the wrapped body" do
      closed = false
      wrapped = Object.new
      wrapped.define_singleton_method(:each) { |&b| b.call("x") }
      wrapped.define_singleton_method(:close) { closed = true }

      RequestTimeout::TimedBody.new(wrapped, deadline: clock(1000), seconds: 1000).close

      _(closed).must_equal(true)
    end
  end

  describe "#call" do
    let(:downstream) { ->(_env) { [200, { "content-type" => "text/plain" }, ["ok"]] } }

    it "passes status and headers through unchanged" do
      status, headers, _body = RequestTimeout.new(downstream, seconds: 1000).call({})

      _(status).must_equal(200)
      _(headers).must_equal({ "content-type" => "text/plain" })
    end

    it "wraps the body so a slow stream is bounded" do
      app = ->(_env) { [200, {}, ["chunk"]] }

      _, _, body = RequestTimeout.new(app, seconds: 0).call({})

      # seconds: 0 means the deadline is already in the past by the time we iterate.
      _ { body.each { |_| } }.must_raise(RequestTimeout::Error)
    end
  end
end
