# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/cbi"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe CBI do
      before do
        VCR.insert_cassette("cbi", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { CBI.new }

      it "fetches rates with IQD as the quote" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 1), upto: Date.new(2026, 2, 28))

        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["IQD"])
      end

      it "fetches multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 1), upto: Date.new(2026, 2, 28))
        dates = dataset.map { |r| r[:date] }.uniq
        sample = dataset.select { |r| r[:date] == dates.first }

        _(sample.size).must_be(:>, 5)
      end

      it "returns USD/IQD around the official peg" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 10), upto: Date.new(2026, 2, 11))
        usd = dataset.find { |r| r[:base] == "USD" && r[:date] == Date.new(2026, 2, 11) }

        _(usd).wont_be_nil
        _(usd[:rate]).must_be_close_to(1310, 30)
      end

      it "filters by date range" do
        dataset = adapter.fetch(after: Date.new(2026, 2, 5), upto: Date.new(2026, 2, 10))

        dates = dataset.map { |r| r[:date] }.uniq

        _(dates.min).must_be(:>, Date.new(2026, 2, 5))
        _(dates.max).must_be(:<=, Date.new(2026, 2, 10))
      end

      describe "URL discovery" do
        it "picks the xlsx link whose anchor text mentions gold (multi-currency daily file)" do
          html = <<~HTML
            <a href="https://cbi.iq/static/uploads/up/file-111.xlsx">الدينار تجاه الدولار</a>
            <a href="https://cbi.iq/static/uploads/up/file-222.xlsx">العملات الرئيسية والذهب</a>
            <a href="https://cbi.iq/static/uploads/up/file-333.xlsx">العملات الأجنبية تجاه الدولار</a>
          HTML

          url = adapter.send(:discover_file_url, html)

          _(url).must_equal("https://cbi.iq/static/uploads/up/file-222.xlsx")
        end

        it "raises when no xlsx link mentions gold" do
          html = '<a href="https://cbi.iq/static/uploads/up/file-111.xlsx">USD only</a>'

          assert_raises(RuntimeError) { adapter.send(:discover_file_url, html) }
        end
      end

      describe "currency code extraction" do
        it "maps S.FR to CHF" do
          _(adapter.send(:extract_code, "S.FR")).must_equal("CHF")
        end

        it "maps UAE to AED" do
          _(adapter.send(:extract_code, "UAE")).must_equal("AED")
        end

        it "maps Gold to XAU" do
          _(adapter.send(:extract_code, "Gold")).must_equal("XAU")
        end

        it "maps SDR to XDR" do
          _(adapter.send(:extract_code, "SDR")).must_equal("XDR")
        end

        it "extracts the trailing ISO code from a multi-word header" do
          _(adapter.send(:extract_code, "Saudi Arabian Riyal SAR")).must_equal("SAR")
        end
      end

      describe "month label parsing" do
        it "parses 'Jan. 2026'" do
          _(adapter.send(:month_from_label, "Jan. 2026")).must_equal(1)
        end

        it "parses 'Feb.2026'" do
          _(adapter.send(:month_from_label, "Feb.2026")).must_equal(2)
        end

        it "parses 'Dec, 2009'" do
          _(adapter.send(:month_from_label, "Dec, 2009")).must_equal(12)
        end

        it "returns nil for non-month labels" do
          _(adapter.send(:month_from_label, "Average")).must_be_nil
          _(adapter.send(:month_from_label, "2026")).must_be_nil
          _(adapter.send(:month_from_label, "1")).must_be_nil
        end
      end
    end
  end
end
