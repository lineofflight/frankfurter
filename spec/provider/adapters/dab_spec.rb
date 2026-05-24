# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/dab"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe DAB do
      before do
        VCR.insert_cassette("dab", match_requests_on: [:method, :host])
      end

      after { VCR.eject_cassette }

      let(:adapter) { DAB.new }

      it "fetches rates for a date range" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 18), upto: Date.new(2026, 5, 20))

        _(dataset).wont_be_empty
      end

      it "returns multiple currencies per date" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 20))

        _(dataset.map { |r| r[:date] }.uniq.size).must_equal(1)
        _(dataset.size).must_be(:>, 5)
      end

      it "uses AFN as quote and foreign currency as base" do
        dataset = adapter.fetch(after: Date.new(2026, 5, 20), upto: Date.new(2026, 5, 20))

        _(dataset.map { |r| r[:quote] }.uniq).must_equal(["AFN"])
        _(dataset).wont_be_empty
        _(dataset.map { |r| r[:base] }).must_include("USD")
      end

      it "parses the daily table as the transfer mid" do
        html = <<~HTML
          <div class="table-responsive">
            <table class="table table-striped">
              <thead>
                <tr>
                  <th>Currency</th>
                  <th>Cash (Sell)</th>
                  <th>Cash (Buy)</th>
                  <th>Transfer (Sell)</th>
                  <th>Transfer (Buy)</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>USD$</td>
                  <td>63.7940</td>
                  <td>63.5940</td>
                  <td>63.7440</td>
                  <td>63.6440</td>
                </tr>
              </tbody>
            </table>
          </div>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
        _(records.first[:quote]).must_equal("AFN")
        _(records.first[:rate]).must_be_close_to(63.694, 0.0001)
      end

      it "maps descriptive currency labels to ISO codes" do
        html = <<~HTML
          <div class="table-responsive">
            <table class="table table-striped">
              <tbody>
                <tr><td>EURO€</td><td>74</td><td>73</td><td>73.5</td><td>73.0</td></tr>
                <tr><td>POUND£</td><td>86</td><td>85</td><td>85.5</td><td>85.0</td></tr>
                <tr><td>SWISS₣</td><td>82</td><td>81</td><td>81.5</td><td>81.0</td></tr>
                <tr><td>INDIAN Rs.</td><td>0.76</td><td>0.74</td><td>0.755</td><td>0.745</td></tr>
                <tr><td>PAKISTAN Rs.</td><td>0.23</td><td>0.21</td><td>0.225</td><td>0.215</td></tr>
                <tr><td>CNY¥</td><td>9.9</td><td>9.5</td><td>9.8</td><td>9.6</td></tr>
                <tr><td>UAE DIRHAM</td><td>17.3</td><td>17.1</td><td>17.25</td><td>17.15</td></tr>
                <tr><td>SAUDI RIYAL</td><td>16.7</td><td>16.6</td><td>16.68</td><td>16.62</td></tr>
              </tbody>
            </table>
          </div>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))
        codes = records.map { |r| r[:base] }

        _(codes).must_equal(["EUR", "GBP", "CHF", "INR", "PKR", "CNY", "AED", "SAR"])
      end

      it "converts IRAN Toman to IRR by dividing by 10" do
        html = <<~HTML
          <div class="table-responsive">
            <table class="table table-striped">
              <tbody>
                <tr>
                  <td>IRAN Toman</td>
                  <td>0.0009</td>
                  <td>0.0003</td>
                  <td>0.0008</td>
                  <td>0.0004</td>
                </tr>
              </tbody>
            </table>
          </div>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("IRR")
        _(records.first[:quote]).must_equal("AFN")
        # Transfer mid for Toman: (0.0008 + 0.0004) / 2 = 0.0006
        # IRR = Toman / 10 = 0.00006
        _(records.first[:rate]).must_be_close_to(0.00006, 1e-7)
      end

      it "ignores the Average Rates table" do
        html = <<~HTML
          <div class="table-responsive">
            <table class="table table-striped">
              <tbody>
                <tr><td>USD$</td><td>63.79</td><td>63.59</td><td>63.74</td><td>63.64</td></tr>
              </tbody>
            </table>
          </div>
          <div class="table-responsive">
            <table class="table table-striped">
              <tbody>
                <tr><td>USD$</td><td>64.83</td><td>64.63</td><td>64.78</td><td>64.68</td></tr>
              </tbody>
            </table>
          </div>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:rate]).must_be_close_to(63.69, 0.01)
      end

      it "returns empty for no-data responses" do
        html = '<div class="messages">There were no results.</div>'
        records = adapter.parse(html, date: Date.new(2021, 1, 15))

        _(records).must_be_empty
      end

      it "ignores rows with empty cells" do
        html = <<~HTML
          <div class="table-responsive">
            <table class="table table-striped">
              <tbody>
                <tr><td></td><td></td><td></td><td></td><td></td></tr>
                <tr><td>USD$</td><td>63.79</td><td>63.59</td><td>63.74</td><td>63.64</td></tr>
              </tbody>
            </table>
          </div>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
      end

      it "ignores unknown currency labels" do
        html = <<~HTML
          <div class="table-responsive">
            <table class="table table-striped">
              <tbody>
                <tr><td>MARTIAN credit</td><td>1</td><td>1</td><td>1</td><td>1</td></tr>
                <tr><td>USD$</td><td>63.79</td><td>63.59</td><td>63.74</td><td>63.64</td></tr>
              </tbody>
            </table>
          </div>
        HTML
        records = adapter.parse(html, date: Date.new(2026, 5, 20))

        _(records.length).must_equal(1)
        _(records.first[:base]).must_equal("USD")
      end
    end
  end
end
