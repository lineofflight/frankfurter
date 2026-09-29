# frozen_string_literal: true

# Runs one Ruby adapter call under a VCR cassette and prints its rows as golden JSON for the Go parity tests.
#
#   APP_ENV=test mise exec -- bundle exec ruby go/scripts/golden.rb [options] KEY CASSETTE MATCH 'CALL' > FILE
#
#   KEY       adapter key, any case (jpc)
#   CASSETTE  cassette name in spec/vcr_cassettes, without .yml (jpc)
#   MATCH     match_requests_on, comma-separated (method,uri)
#   CALL      Ruby expression evaluated on a new adapter instance
#             ('fetch(after: Date.new(2026, 9, 20), upto: Date.new(2026, 9, 27))')
#
#   --repeats           allow_playback_repeats: true
#   --today YYYY-MM-DD  stub Date.today, as a spec's Date.stub(:today, ...) does
#
# Run from the repository root. It configures VCR and WebMock itself and never loads spec/helper.rb, which would
# reseed the test database. APP_ENV=test is still required: it stubs adapter sleeps and points lib/db.rb at the test
# database, which adapters only read.

require "json"
require "optparse"

abort "golden.rb: run with APP_ENV=test" unless ENV["APP_ENV"] == "test"

options = { repeats: false, today: nil }
OptionParser.new do |o|
  o.banner = "usage: golden.rb [--repeats] [--today YYYY-MM-DD] KEY CASSETTE MATCH CALL"
  o.on("--repeats") { options[:repeats] = true }
  o.on("--today DATE") { |d| options[:today] = d }
end.parse!

abort "usage: golden.rb [--repeats] [--today YYYY-MM-DD] KEY CASSETTE MATCH CALL" unless ARGV.size == 4
key, cassette, match, call = ARGV
match_requests_on = match.split(",").map { |m| m.strip.to_sym }

require_relative "../../boot"
require "bigdecimal"
require "date"
require "vcr"
require "webmock"
require "provider"
require "provider/adapters/#{key.downcase}"

# Credentials the recording replaced with placeholders. VCR swaps each placeholder back for the variable's value on
# playback, so any value works as long as the adapter sends the same one.
SECRETS = ["TCMB_API_KEY", "FRED_API_KEY", "BAM_API_KEY", "BANXICO_API_KEY", "BCCH_USER", "BCCH_PASS", "BOT_API_KEY"].freeze

VCR.configure do |c|
  c.cassette_library_dir = File.expand_path("../../spec/vcr_cassettes", __dir__)
  c.hook_into(:webmock)
  SECRETS.each do |name|
    ENV[name] ||= "golden-#{name.downcase}"
    c.filter_sensitive_data("<#{name}>") { ENV[name] }
  end
end

if options[:today]
  today = Date.iso8601(options[:today])
  Date.singleton_class.prepend(Module.new { define_method(:today) { |*| today } })
end

def plain(value)
  case value
  when Date then value.iso8601
  when BigDecimal, Rational, Integer then value.to_f
  when Float
    raise "golden.rb: non-finite rate #{value}" unless value.finite?

    value
  when Symbol then value.to_s
  else value
  end
end

adapter = Provider::Adapters.const_get(key.upcase).new
rows = VCR.use_cassette(cassette, match_requests_on:, allow_playback_repeats: options[:repeats]) do
  adapter.instance_eval(call)
end

rows = rows.map { |row| row.to_h { |k, v| [k.to_s, plain(v)] }.compact.sort.to_h }
rows.sort_by! { |row| [row["date"].to_s, row["base"].to_s, row["quote"].to_s, JSON.generate(row)] }

puts JSON.pretty_generate({
  "adapter" => key.upcase,
  "cassette" => cassette,
  "match_requests_on" => match_requests_on.map(&:to_s),
  "allow_playback_repeats" => options[:repeats],
  "today" => options[:today],
  "call" => call,
  "rows" => rows,
})
