# frozen_string_literal: true

require_relative "helper"

describe WebBotAuth do
  # The Ed25519 test key of RFC 9421, appendix B.1.4, which the Web Bot Auth drafts' and Cloudflare's test vectors sign
  # with.
  let(:rfc_key) do
    <<~PEM
      -----BEGIN PRIVATE KEY-----
      MC4CAQAwBQYDK2VwBCIEIJ+DYvh6SEqVTm50DFtMDoQikTmiCqirVv9mWG9qfSnF
      -----END PRIVATE KEY-----
    PEM
  end
  let(:rfc_keyid) { "poqkLGiymh_W0uP6PZFw-dvez3QJT5SolqXBCW38r0U" }

  it "is off in tests by default" do
    refute_predicate(WebBotAuth, :configured?)
  end

  it "names the key by its JWK thumbprint and lists it without the private part" do
    WebBotAuth.private_key = rfc_key

    assert_equal(rfc_keyid, WebBotAuth.keyid)
    expected = {
      "keys" => [
        {
          "kty" => "OKP",
          "crv" => "Ed25519",
          "kid" => rfc_keyid,
          "x" => "JrQLj5P_89iXES9-vFgrIy29clF9CC_oPPsw3c5D0bs",
          "use" => "sig",
        },
      ],
    }

    assert_equal(expected, Oj.load(WebBotAuth.directory))
  end

  # Cloudflare's web-bot-auth test vector for a request to example.com with Signature-Agent
  # "https://signature-agent.test", the bare-string form Cloudflare verifies.
  it "signs a request as the reference implementation does" do
    WebBotAuth.private_key = rfc_key
    nonce = "e8N7S2MFd/qrd6T2R3tdfAuuANngKI7LFtKYI/vowzk4lAZYadIX6wW25MwG7DCT9RUKAJ0qVkU0mEeLElW1qg=="
    headers = WebBotAuth.send(
      :sign,
      "sig2",
      { '"@authority"' => "example.com", '"signature-agent"' => '"https://signature-agent.test"' },
      created: 1_735_689_600,
      expires: 1_735_693_200,
      nonce:,
      tag: "web-bot-auth",
    )

    input = 'sig2=("@authority" "signature-agent");created=1735689600;' \
            "keyid=\"#{rfc_keyid}\";alg=\"ed25519\";expires=1735693200;nonce=\"#{nonce}\";tag=\"web-bot-auth\""

    assert_equal(input, headers["Signature-Input"])
    assert_equal(
      "sig2=:jdq0SqOwHdyHr9+r5jw3iYZH6aNGKijYp/EstF4RQTQdi5N5YYKrD+mCT1HA1nZDsi6nJKuHxUi/5Syp3rLWBA==:",
      headers["Signature"],
    )
  end

  # Cloudflare's test vector for a signed directory, as draft-ietf-webbotauth-httpsig-protocol-01 (E.2.3) has it.
  it "signs the directory as the reference implementation does" do
    WebBotAuth.private_key = rfc_key
    body = WebBotAuth.directory
    headers = WebBotAuth.directory_headers(
      body,
      authority: "signature-agent.test",
      created: 1_735_689_600,
      expires: 4_889_289_600,
    )

    assert_equal("sha-256=:CADMT2aBdV/rqQr/NIru64ERQkCobVvllA4V0fLFDu0=:", headers["Content-Digest"])
    assert_equal(
      'binding0=("@authority";req "content-digest");created=1735689600;' \
      "keyid=\"#{rfc_keyid}\";alg=\"ed25519\";expires=4889289600;" \
      'tag="http-message-signatures-directory"',
      headers["Signature-Input"],
    )
    assert_equal(
      "binding0=:yiHq0TXrbpzbmlttAQMpYoAufitFJUWuNsakB7QQMoN0EHbo5o51bZRVR8az/ptTWCwllix9clrKXfGKwdPzBg==:",
      headers["Signature"],
    )
  end

  it "signs a request with the public key for five minutes" do
    key = turn_on_web_bot_auth
    now = 1_735_689_600
    headers = Time.stub(:now, Time.at(now)) { WebBotAuth.request_headers("Bank.Example") }

    assert_equal('"https://api.frankfurter.dev"', headers["Signature-Agent"])
    assert_includes(headers["Signature-Input"], ";created=#{now};")
    assert_includes(headers["Signature-Input"], ";expires=#{now + 300};")
    assert_includes(headers["Signature-Input"], ";keyid=\"#{WebBotAuth.keyid}\";")
    assert_signed(
      headers,
      { '"@authority"' => "bank.example", '"signature-agent"' => '"https://api.frankfurter.dev"' },
      key,
    )
  end

  it "gives every request a fresh nonce" do
    turn_on_web_bot_auth

    nonces = Array.new(2) { WebBotAuth.request_headers("bank.example")["Signature-Input"][/nonce="([^"]+)"/, 1] }

    assert_equal(2, nonces.uniq.size)
  end

  it "accepts a single-line PEM with escaped newlines for Docker env files" do
    WebBotAuth.private_key = rfc_key.strip.gsub("\n", '\n')

    assert_equal(rfc_keyid, WebBotAuth.keyid)
  end

  it "rejects a key that is not Ed25519" do
    WebBotAuth.private_key = OpenSSL::PKey::EC.generate("prime256v1").private_to_pem

    assert_raises(ArgumentError) { WebBotAuth.request_headers("bank.example") }
  end
end
