# frozen_string_literal: true

# Run against each local snapshot with DATABASE_URL; compare status, body digests and median timings in the JSONL files.
require_relative "../boot"
require "app"
require "rack/mock"
require "digest"
require "json"

paths = [
  "/v1/latest", "/v1/2009-08-09?from=EUR&to=USD,GBP",
  "/v1/2009-08-01..2009-08-31?from=EUR&to=USD,GBP",
  "/v2/rates", "/v2/rates?base=EUR&quotes=USD,GBP,JPY",
  "/v2/rates?base=USD&quotes=EUR,GBP&date=2009-08-09",
  "/v2/rates?base=USD&quotes=EUR,GBP&from=2009-08-01&to=2009-08-31",
  "/v2/rates?base=USD&quotes=EUR,GBP&from=2009-01-01&to=2010-01-01&group=week",
  "/v2/rates?base=USD&quotes=EUR,GBP&from=2009-01-01&to=2010-01-01&group=month",
  "/v2/rates?base=USD&quotes=EUR,GBP&date=2009-08-09&expand=providers",
  "/v2/rates?base=USD&quotes=EUR,GBP&from=2009-08-01&to=2009-08-31&expand=providers",
  "/v2/currencies", "/v2/providers",
]
["cbe", "cbtt", "boja", "boz", "tcmb", "cbo", "cbllr", "dab", "cbi", "bam", "bcbo", "bi", "bcu", "bm", "cbvs",
 "nrb",].each do |provider|
  paths << "/v2/providers/#{provider}/rates"
end
{
  "cbi" => ["XDR", "IQD", "2009-08-01", "2009-08-31"],
  "boz" => ["USD", "ZMK", "2010-01-01", "2010-12-31"],
  "tcmb" => ["JPY", "TRY", "2026-03-01", "2026-03-22"],
  "cbvs" => ["GYD", "SRD", "2026-09-07", "2026-09-08"],
}.each do |provider, (base, quote, from, to)|
  query = "base=#{base}&quotes=#{quote}&from=#{from}&to=#{to}"
  paths << "/v2/providers/#{provider}/rates?#{query}"
  paths << "/v2/providers/#{provider}/rates?#{query}&group=week"
  paths << "/v2/providers/#{provider}/rates?#{query}&group=month"
  paths << "/v2/providers/#{provider}/rate/#{base}/#{quote}?date=#{to}"
end

request = Rack::MockRequest.new(App.app)
paths.each do |path|
  request.get(path)
  result = nil
  durations = Array.new(3) do
    start = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    result = request.get(path)
    (Process.clock_gettime(Process::CLOCK_MONOTONIC) - start) * 1000
  end
  puts JSON.generate(path:, status: result.status, bytes: result.body.bytesize,
                     digest: Digest::SHA256.hexdigest(result.body), milliseconds: durations.sort[1].round(3),)
  $stdout.flush
end
