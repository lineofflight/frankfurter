# frozen_string_literal: true

require "digest"
require "http"
require "oj"
require "openssl"
require "securerandom"

# Web Bot Auth: our requests to upstream providers carry an HTTP Message Signature (RFC 9421) made with our Ed25519 key,
# so a source or the CDN in front of it can tell them from anyone else's that borrow our User-Agent. The key is
# WEB_BOT_AUTH_PRIVATE_KEY, in PEM; WEB_BOT_AUTH_AUTHORITY names the host serving the key directory and defaults to
# api.frankfurter.dev, so self-hosters can bring their own key and host without impersonating ours. Without a key
# requests go unsigned and the key directory answers 404.
#
# Signatures follow what Cloudflare verifies and draft-ietf-webbotauth-httpsig-protocol-01 where the two agree. They
# differ on Signature-Agent: the draft wants a dictionary naming the directory's own URL, while Cloudflare fails
# anything but the older bare string naming the origin whose well-known path serves the directory. We send the bare
# string.
module WebBotAuth
  DEFAULT_AUTHORITY = "api.frankfurter.dev"
  DIRECTORY = "/.well-known/http-message-signatures-directory"
  MEDIA_TYPE = "application/http-message-signatures-directory+json"
  USER_AGENT = "Mozilla/5.0 (compatible; Frankfurter; +https://frankfurter.dev/bot)"

  # How long a request's signature holds: room for a slow request and a clock a little off. The draft recommends under a
  # day, Cloudflare a minute.
  LIFETIME = 300
  # How long the directory's signature holds, well past a day's caching.
  DIRECTORY_LIFETIME = 604_800

  class << self
    # Tests set these in place of ENV, or false for no key.
    attr_writer :private_key, :authority

    def private_key
      return @private_key unless @private_key.nil?

      ENV["WEB_BOT_AUTH_PRIVATE_KEY"]
    end

    # The host that serves our key at DIRECTORY, and the only authority the directory is ever signed for, whatever Host
    # a request names.
    def authority
      value = @authority.nil? ? ENV["WEB_BOT_AUTH_AUTHORITY"] : @authority
      value = DEFAULT_AUTHORITY if value.nil? || value.strip.empty?
      value.downcase
    end

    def signature_agent
      %("https://#{authority}")
    end

    def configured?
      private_key.is_a?(String) && !private_key.strip.empty?
    end

    def ours?
      configured? && authority == DEFAULT_AUTHORITY
    end

    def key
      pem = private_key
      cached_pem, cached_key = @cache
      return cached_key if cached_pem == pem

      key = OpenSSL::PKey.read(pem.gsub('\n', "\n"))
      raise ArgumentError, "WEB_BOT_AUTH_PRIVATE_KEY is not an Ed25519 key" unless key.oid == "ED25519"

      @cache = [pem, key]
      key
    end

    # The key's JWK thumbprint (RFC 7638, RFC 8037), which names it in signatures and in the directory.
    def keyid
      jwk = Oj.dump({ crv: "Ed25519", kty: "OKP", x: }, mode: :compat)
      urlsafe_encode64(Digest::SHA256.digest(jwk))
    end

    # The directory's body: our public key as a JWK set.
    def directory
      Oj.dump({ keys: [{ kty: "OKP", crv: "Ed25519", kid: keyid, x:, use: "sig" }] }, mode: :compat)
    end

    # The headers that sign a request to target_authority: its host, with the port where it isn't the scheme's default.
    def request_headers(
      target_authority,
      created: Time.now.to_i,
      expires: created + LIFETIME,
      nonce: [SecureRandom.random_bytes(64)].pack("m0")
    )
      agent = signature_agent
      components = { '"@authority"' => target_authority.downcase, '"signature-agent"' => agent }
      sign("sig1", components, created:, expires:, nonce:, tag: "web-bot-auth")
        .merge("Signature-Agent" => agent)
    end

    # The headers that sign the directory's body as served at authority, so a verifier knows whoever serves it holds the
    # key. The authority is configured, never a request's Host, or anyone could have the key sign a directory for a host
    # of their choosing. Only the test vectors pass another.
    def directory_headers(
      body,
      authority: self.authority,
      created: Time.now.to_i,
      expires: created + DIRECTORY_LIFETIME
    )
      digest = "sha-256=:#{Digest::SHA256.base64digest(body)}:"
      components = { '"@authority";req' => authority, '"content-digest"' => digest }
      sign("binding0", components, created:, expires:, tag: "http-message-signatures-directory")
        .merge("Content-Digest" => digest)
    end

    private

    def x
      urlsafe_encode64(key.raw_public_key)
    end

    def urlsafe_encode64(bytes)
      [bytes].pack("m0").tr("+/", "-_").delete("=")
    end

    # An RFC 9421 signature over the components, with its parameters in the order of Cloudflare's test vectors.
    def sign(label, components, created:, expires:, tag:, nonce: nil)
      params = "(#{components.keys.join(" ")});created=#{created};keyid=\"#{keyid}\";alg=\"ed25519\";" \
               "expires=#{expires}#{";nonce=\"#{nonce}\"" if nonce};tag=\"#{tag}\""
      base = components.map { |name, value| "#{name}: #{value}" }.push(%("@signature-params": #{params})).join("\n")

      {
        "Signature-Input" => "#{label}=#{params}",
        "Signature" => "#{label}=:#{[key.sign(nil, base)].pack("m0")}:",
      }
    end
  end

  # Signs each request Adapter#http sends once there is a key. http.rb has set the Host header by now, which is the
  # authority a verifier checks.
  class Signer < HTTP::Feature
    def wrap_request(request)
      request.headers.merge!(WebBotAuth.request_headers(request.headers["Host"])) if WebBotAuth.configured?
      request
    end

    HTTP::Options.register_feature(:web_bot_auth, self)
  end
end
