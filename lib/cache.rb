# frozen_string_literal: true

require "net/http"

# Purges the CDN cache after data imports. Currently supports Cloudflare.
# No-ops when credentials are not configured.
class Cache
  class << self
    def purge
      new.purge
    end
  end

  attr_reader :zone_id, :api_token

  def initialize(zone_id: ENV["CLOUDFLARE_ZONE_ID"], api_token: ENV["CLOUDFLARE_API_TOKEN"])
    @zone_id = zone_id
    @api_token = api_token
  end

  def purge
    return unless configured?

    uri = URI("https://api.cloudflare.com/client/v4/zones/#{zone_id}/purge_cache")
    req = Net::HTTP::Post.new(uri)
    req["Authorization"] = "Bearer #{api_token}"
    req["Content-Type"] = "application/json"
    req.body = '{"purge_everything":true}'

    Net::HTTP.start(uri.hostname, uri.port, use_ssl: true) { |http| http.request(req) }
  end

  private

  def configured?
    zone_id && api_token
  end
end
