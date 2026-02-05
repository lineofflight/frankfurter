# frozen_string_literal: true

require "net/http"
require "json"
require "uri"

class FXService
  API_BASE = "https://api.frankfurter.dev/v1"
  FALLBACK_FILE = File.expand_path("../../data/sample_fx.json", __FILE__)
  MAX_RETRIES = 3
  RETRY_DELAY = 1
  CACHE_TTL = 300 # 5 minutes

  @cache = {}
  @cache_mutex = Mutex.new

  class << self
    attr_reader :cache, :cache_mutex
  end

  def self.fetch_range(start_date, end_date, from: "EUR", to: "USD")
    new.fetch_range(start_date, end_date, from:, to:)
  end

  def fetch_range(start_date, end_date, from: "EUR", to: "USD")
    cache_key = "#{start_date}_#{end_date}_#{from}_#{to}"

    cached_data = get_from_cache(cache_key)
    return cached_data if cached_data

    url = "#{API_BASE}/#{start_date}..#{end_date}?from=#{from}&to=#{to}"
    data = fetch_with_retry(url)

    result = if data.nil?
               parse_fallback
             else
               parse_response(data)
             end

    set_cache(cache_key, result)
    result
  rescue StandardError => e
    warn "Error fetching FX data: #{e.message}"
    parse_fallback
  end

  private

  def fetch_with_retry(url, retries = MAX_RETRIES)
    uri = URI(url)
    response = nil

    retries.times do |attempt|
      begin
        http = Net::HTTP.new(uri.host, uri.port)
        http.use_ssl = true
        http.read_timeout = 5
        http.open_timeout = 5

        request = Net::HTTP::Get.new(uri)
        response = http.request(request)

        return response.body if response.code == "200"

        sleep RETRY_DELAY * (attempt + 1)
      rescue StandardError => e
        warn "Attempt #{attempt + 1} failed: #{e.message}"
        sleep RETRY_DELAY * (attempt + 1) if attempt < retries - 1
      end
    end

    nil
  end

  def parse_response(json_string)
    data = JSON.parse(json_string)
    {
      base: data["base"],
      start_date: data["start_date"],
      end_date: data["end_date"],
      rates: data["rates"] || {}
    }
  end

  def parse_fallback
    return {} unless File.exist?(FALLBACK_FILE)

    data = JSON.parse(File.read(FALLBACK_FILE))
    {
      base: data["base"],
      start_date: data["start_date"],
      end_date: data["end_date"],
      rates: data["rates"] || {}
    }
  end

  def get_from_cache(key)
    self.class.cache_mutex.synchronize do
      entry = self.class.cache[key]
      return nil unless entry

      return nil if Time.now - entry[:timestamp] > CACHE_TTL

      entry[:data]
    end
  end

  def set_cache(key, data)
    self.class.cache_mutex.synchronize do
      self.class.cache[key] = {
        data:,
        timestamp: Time.now
      }
    end
  end
end
