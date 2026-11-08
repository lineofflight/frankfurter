# frozen_string_literal: true

require_relative "helper"
require "tmpdir"
load File.expand_path("../bin/api_snapshot", __dir__)

describe ApiSnapshot do
  after do
    ApiSnapshot.unpin_clock! if ApiSnapshot.respond_to?(:unpin_clock!)
  end

  describe ".pin_clock!" do
    it "pins Date.today, Time.now and DateTime.now to the given timestamp" do
      ApiSnapshot.pin_clock!("2026-09-13T15:30:00Z")

      _(Date.today).must_equal(Date.new(2026, 9, 13))
      _(Time.now.utc).must_equal(Time.utc(2026, 9, 13, 15, 30, 0))
      _(DateTime.now.to_date).must_equal(Date.new(2026, 9, 13))
    end

    it "accepts a bare date string and defaults to noon UTC" do
      normalized = ApiSnapshot.pin_clock!("2026-09-13")

      _(normalized).must_equal("2026-09-13T12:00:00Z")
      _(Date.today).must_equal(Date.new(2026, 9, 13))
      _(Time.now.utc).must_equal(Time.utc(2026, 9, 13, 12, 0, 0))
    end
  end

  describe ".generate_requests" do
    it "includes provider, blend, currency, v1 and NBP-specific requests" do
      requests = ApiSnapshot.generate_requests(DB)

      _(requests).must_include("/v2/providers")
      _(requests).must_include("/v2/currencies")
      _(requests).must_include("/v2/currencies?providers=ECB")
      _(requests).must_include("/v2/currency/USD")
      _(requests).must_include("/v2/providers/ECB")
      _(requests).must_include("/v2/providers/ECB/rates")
      _(requests).must_include("/v2/rates")
      _(requests).must_include("/v2/rates?expand=providers")
      _(requests).must_include("/v2/rates.csv?expand=providers")
      _(requests).must_include("/v2/rate/EUR/USD")
      _(requests).must_include("/v1/latest")
      _(requests).must_include("/v1/currencies")
      _(requests).must_include("/v2/providers/NBP/rate/PLN/AFN?date=2026-09-09")
      _(requests).must_include("/v2/providers/NBP/rate/PLN/AFN?date=2026-09-10")
      _(requests).must_include("/v2/rates?providers=NBP,ECB")
    end
  end

  describe ".write_requests_file and .read_requests_file" do
    it "round-trips the request list and pinned clock" do
      Dir.mktmpdir do |dir|
        path = File.join(dir, "requests.txt")
        ApiSnapshot.write_requests_file(path, ["/v2/providers", "/v2/rates"], "2026-09-13T12:00:00Z")
        requests, clock = ApiSnapshot.read_requests_file(path)

        _(clock).must_equal("2026-09-13T12:00:00Z")
        _(requests).must_equal(["/v2/providers", "/v2/rates"])
      end
    end
  end

  describe ".format_response" do
    it "records status, captured headers and body while omitting Date" do
      headers = {
        "content-type" => "text/csv",
        "cache-control" => "public, max-age=86400",
        "etag" => '"abc123"',
        "content-disposition" => 'attachment; filename="rates.csv"',
        "date" => "Sun, 13 Sep 2026 12:00:00 GMT",
      }
      response = Struct.new(:status, :headers, :body) do
        def get_header(name)
          headers[name]
        end
      end.new(200, headers, "date,base,quote,rate\n")

      formatted = ApiSnapshot.format_response("/v2/rates.csv", response)

      _(formatted).must_include("Request: GET /v2/rates.csv\n")
      _(formatted).must_include("Status: 200\n")
      _(formatted).must_include("Content-Type: text/csv\n")
      _(formatted).must_include("Cache-Control: public, max-age=86400\n")
      _(formatted).must_include("ETag: \"abc123\"\n")
      _(formatted).must_include("Content-Disposition: attachment; filename=\"rates.csv\"\n")
      _(formatted).wont_include("Date:")
      _(formatted.end_with?("\n\ndate,base,quote,rate\n")).must_equal(true)
    end
  end

  describe ".diff_dirs and .find_matching_files" do
    it "summarizes identical, differing and missing responses and finds matches" do
      Dir.mktmpdir do |dir|
        dir_a = File.join(dir, "a")
        dir_b = File.join(dir, "b")
        FileUtils.mkdir_p([dir_a, dir_b])

        file1 = "0001_v2_providers.http"
        file2 = "0002_v2_rates.http"
        file3 = "0003_v1_latest.http"

        File.write(File.join(dir_a, file1), "Request: GET /v2/providers\nStatus: 200\n\n[]")
        File.write(File.join(dir_b, file1), "Request: GET /v2/providers\nStatus: 200\n\n[]")

        File.write(File.join(dir_a, file2), "Request: GET /v2/rates\nStatus: 200\n\n[1]")
        File.write(File.join(dir_b, file2), "Request: GET /v2/rates\nStatus: 200\n\n[2]")

        File.write(File.join(dir_a, file3), "Request: GET /v1/latest\nStatus: 200\n\n{}")

        report = ApiSnapshot.diff_dirs(dir_a, dir_b)

        _(report[:total]).must_equal(3)
        _(report[:identical]).must_equal(1)
        _(report[:differing].map { |d| d[:name] }).must_equal([file2])
        _(report[:missing].map { |m| m[:name] }).must_equal([file3])

        _(ApiSnapshot.find_matching_files(dir_a, dir_b, "2")).must_equal([file2])
        _(ApiSnapshot.find_matching_files(dir_a, dir_b, "/v2/rates")).must_equal([file2])
        _(ApiSnapshot.find_matching_files(dir_a, dir_b, "providers")).must_equal([file1])
      end
    end
  end
end
