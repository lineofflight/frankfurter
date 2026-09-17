# frozen_string_literal: true

require_relative "helper"
require_relative "../bin/provider_health"

# Locks the staleness calibration: thresholds must catch genuinely-frozen feeds (e.g. NBC at 10 missed) while tolerating
# normal lag (FBIL holidays at 4, T+1 at 1, monthly archives in arrears). See bin/provider_health.rb.
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

  describe "unknown currencies" do
    it "flags an up-to-date provider with unknown codes" do
      entry = provider("CBKKW", "daily", 0).merge("unknown_currencies" => ["ECS", "WAUA"])

      _(flagged([entry])).must_equal([entry])
    end

    it "flags unknown codes from historical-only providers" do
      entry = provider("BBK", nil, nil).merge("unknown_currencies" => ["ZZZ"])

      _(flagged([entry])).must_equal([entry])
    end

    it "names codes and remediation without claiming the feed is stale" do
      entry = provider("CBKKW", "daily", 0).merge("unknown_currencies" => ["ECS", "WAUA"])
      body = render_body(entry, "2026-09-17")

      _(body).must_include("CBKKW")
      _(body).must_include("ECS")
      _(body).must_include("WAUA")
      _(body).must_include("currency_patches.json")
      _(body).wont_include("has missed")
    end

    it "reports both staleness and unknown codes in one issue" do
      entry = provider("CBKKW", "daily", 10).merge("unknown_currencies" => ["ECS"])
      body = render_body(entry, "2026-09-17")

      _(body).must_include("has missed")
      _(body).must_include("ECS")
    end
  end

  describe "issue lifecycle" do
    def audit(entries, open_issues)
      commands = []
      stub(:fetch_providers, entries) do
        stub(:open_issues_by_key, open_issues) do
          stub(:gh, ->(*args) { commands << args }) { main }
        end
      end
      commands
    end

    it "opens an issue naming the provider and unknown currency" do
      entry = provider("CBKKW", "daily", 0).merge("unknown_currencies" => ["ECS"])
      commands = audit([entry], {})

      _(commands.size).must_equal(1)
      _(commands.first.take(2)).must_equal(["issue", "create"])
      _(commands.first.last).must_include("CBKKW")
      _(commands.first.last).must_include("ECS")
    end

    it "keeps an existing issue open when publishing recovers but codes remain unknown" do
      entry = provider("CBKKW", "daily", 0).merge("unknown_currencies" => ["ECS"])
      commands = audit([entry], { "CBKKW" => 123 })

      _(commands.map { |args| args.take(3) }).must_equal([["issue", "edit", "123"]])
    end

    it "closes the issue once publishing and currency checks both recover" do
      entry = provider("CBKKW", "daily", 0).merge("unknown_currencies" => [])
      commands = audit([entry], { "CBKKW" => 123 })

      _(commands.map { |args| args.take(3) }).must_equal([
        ["issue", "comment", "123"], ["issue", "close", "123"],
      ])
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
