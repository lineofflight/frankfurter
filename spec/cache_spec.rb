# frozen_string_literal: true

require_relative "helper"
require "cache"

describe Cache do
  it "does nothing when not configured" do
    cache = Cache.new(zone_id: nil, api_token: nil)

    _(cache.purge).must_be_nil
  end

  it "raises when the purge endpoint rejects the request, so it stays pending for retry" do
    WebMock.stub_request(:post, "https://api.cloudflare.com/client/v4/zones/z/purge_cache")
      .to_return(status: 503)

    _ { Cache.new(zone_id: "z", api_token: "t").purge }.must_raise(Net::HTTPFatalError)
  end

  it "succeeds quietly on a 2xx purge response" do
    WebMock.stub_request(:post, "https://api.cloudflare.com/client/v4/zones/z/purge_cache")
      .to_return(status: 200, body: '{"success":true}')

    Cache.new(zone_id: "z", api_token: "t").purge
  end

  describe "debounced purging" do
    def reset_debounce!
      Cache.instance_variable_set(:@pending, false)
      Cache.instance_variable_set(:@last_purge_at, nil)
    end

    # Rewinds the window start so it reads as expired, without stubbing the clock.
    def expire_window!
      Cache.instance_variable_set(:@last_purge_at, Cache.send(:monotonic) - Cache::DEBOUNCE_SECONDS)
    end

    before { reset_debounce! }
    after { reset_debounce! }

    it "purges immediately on the first call of a quiet window" do
      purges = 0
      Cache.stub(:purge, -> { purges += 1 }) { Cache.purge_debounced }

      _(purges).must_equal(1)
    end

    it "coalesces calls inside the window and flushes once it expires" do
      purges = 0
      Cache.stub(:purge, -> { purges += 1 }) do
        Cache.purge_debounced
        Cache.purge_debounced
        Cache.purge_debounced

        _(purges).must_equal(1)

        Cache.purge_pending

        _(purges).must_equal(1)

        expire_window!
        Cache.purge_pending

        _(purges).must_equal(2)

        Cache.purge_pending

        _(purges).must_equal(2)
      end
    end

    it "purges immediately again once the window has expired" do
      purges = 0
      Cache.stub(:purge, -> { purges += 1 }) do
        Cache.purge_debounced
        expire_window!
        Cache.purge_debounced
      end

      _(purges).must_equal(2)
    end

    it "flushes a pending purge regardless of the window when asked" do
      purges = 0
      Cache.stub(:purge, -> { purges += 1 }) do
        Cache.purge_debounced
        Cache.purge_debounced
        Cache.purge_pending(ignore_window: true)
      end

      _(purges).must_equal(2)
    end

    it "does not flush when nothing is pending" do
      purges = 0
      Cache.stub(:purge, -> { purges += 1 }) do
        Cache.purge_debounced
        Cache.purge_pending(ignore_window: true)
      end

      _(purges).must_equal(1)
    end

    it "coalesces callers that arrive while a purge is in flight" do
      purges = 0
      reentrant = lambda do
        purges += 1
        Cache.purge_debounced if purges == 1
      end

      Cache.stub(:purge, reentrant) { Cache.purge_debounced }

      _(purges).must_equal(1)
      _(Cache.instance_variable_get(:@pending)).must_equal(true)
    end

    it "re-marks a failed flush pending, so a later flush retries it" do
      calls = 0
      failing = lambda do
        calls += 1
        raise Net::OpenTimeout if calls == 2
      end

      Cache.stub(:purge, failing) do
        Cache.purge_debounced
        Cache.purge_debounced
        expire_window!

        _ { Cache.purge_pending }.must_raise(Net::OpenTimeout)

        # The failed attempt opened a new window, so the retry waits for it to expire.
        Cache.purge_pending

        _(calls).must_equal(2)

        expire_window!
        Cache.purge_pending
      end

      _(calls).must_equal(3)
    end

    it "re-marks a failed leading purge pending as well" do
      calls = 0
      failing = lambda do
        calls += 1
        raise Net::OpenTimeout if calls == 1
      end

      Cache.stub(:purge, failing) do
        _ { Cache.purge_debounced }.must_raise(Net::OpenTimeout)

        expire_window!
        Cache.purge_pending
      end

      _(calls).must_equal(2)
    end
  end
end
