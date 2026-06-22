# frozen_string_literal: true

require_relative "helper"
require_relative "../bin/provider_health"

# Locks the staleness calibration: thresholds must catch genuinely-frozen feeds
# (e.g. NBC at 10 missed) while tolerating normal lag (FBIL holidays at 4, T+1
# at 1, monthly archives in arrears). See bin/provider_health.rb.
describe "provider_health" do
  def provider(key, cadence, missed)
    {
      "key" => key,
      "name" => "#{key} Bank",
      "publish_cadence" => cadence,
      "publishes_missed" => missed,
      "end_date" => "2026-06-05",
    }
  end

  describe "flagged" do
    it "flags daily providers at or above the threshold, not below" do
      providers = [provider("AT", "daily", 8), provider("BELOW", "daily", 7)]

      _(flagged(providers).map { |p| p["key"] }).must_equal(["AT"])
    end

    it "tolerates normal daily lag — holidays and T+1 publishing" do
      providers = [provider("FBIL", "daily", 4), provider("CBI", "daily", 2), provider("BOJ", "daily", 1)]

      _(flagged(providers)).must_be_empty
    end

    it "flags genuinely frozen daily feeds" do
      providers = [provider("NBC", "daily", 10)]

      _(flagged(providers).map { |p| p["key"] }).must_equal(["NBC"])
    end

    it "flags weekly and monthly at or above two missed buckets" do
      providers = [
        provider("W2", "weekly", 2),
        provider("W1", "weekly", 1),
        provider("M2", "monthly", 2),
        provider("M1", "monthly", 1),
      ]

      _(flagged(providers).map { |p| p["key"] }.sort).must_equal(["M2", "W2"])
    end

    it "never flags historical-only providers with no cadence" do
      providers = [provider("BBK", nil, 9999)]

      _(flagged(providers)).must_be_empty
    end

    it "treats a missing missed-count as zero" do
      providers = [provider("X", "daily", nil)]

      _(flagged(providers)).must_be_empty
    end

    it "orders flagged providers by missed count, descending" do
      providers = [provider("LOW", "daily", 8), provider("HIGH", "daily", 30)]

      _(flagged(providers).map { |p| p["key"] }).must_equal(["HIGH", "LOW"])
    end
  end

  describe "render_body" do
    it "embeds a per-provider marker and the provider's stats" do
      body = render_body(provider("NBC", "daily", 10), "2026-06-22")

      _(body).must_include("<!-- provider-health: NBC -->")
      _(body).must_include("NBC Bank")
      _(body).must_include("| daily | 2026-06-05 | 10 |")
    end

    it "uses no em dash" do
      body = render_body(provider("NBC", "daily", 10), "2026-06-22")

      _(body).wont_include("—")
    end
  end
end
