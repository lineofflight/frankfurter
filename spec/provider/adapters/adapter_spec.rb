# frozen_string_literal: true

require_relative "../../helper"
require "provider/adapters/adapter"

class Provider < Sequel::Model(:providers)
  module Adapters
    describe Adapter do
      it "requires fetch" do
        _ { Class.new(Adapter).new.fetch }.must_raise(NotImplementedError)
      end

      describe ".fetch_each" do
        it "yields records from fetch" do
          fetching_klass = Class.new(Adapter) do
            define_method(:fetch) do |**|
              [{ date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 }]
            end
          end

          batches = []
          fetching_klass.fetch_each { |records| batches << records }

          _(batches.length).must_equal(1)
          _(batches[0].length).must_equal(1)
        end

        it "skips empty results" do
          empty_klass = Class.new(Adapter) do
            define_method(:fetch) do |**|
              []
            end
          end

          batches = []
          empty_klass.fetch_each { |records| batches << records }

          _(batches).must_be_empty
        end

        it "chunks by backfill_range" do
          after = Date.today - 90
          params = []
          chunked_klass = Class.new(Adapter) do
            class << self
              def backfill_range = 30
            end

            define_method(:fetch) do |after: nil, upto: nil|
              params << { after:, upto: }
              [{ date: Date.new(2099, 1, 1), base: "EUR", quote: "USD", rate: 1.1 }]
            end
          end

          chunked_klass.fetch_each(after:) { nil }

          _(params.length).must_equal(4)
          _(params[0][:after]).must_equal(after)
          _(params[0][:upto]).must_equal(after + 29)
          _(params[-1][:upto]).must_be_nil
        end

        it "overlaps consecutive windows by exactly one day" do
          params = []
          chunked_klass = Class.new(Adapter) do
            class << self
              def backfill_range = 30
            end

            define_method(:fetch) do |after: nil, upto: nil|
              params << { after:, upto: }
              []
            end
          end

          chunked_klass.fetch_each(after: Date.today - 100) { nil }

          params.each_cons(2) do |window, following|
            _(following[:after]).must_equal(window[:upto])
          end
        end

        it "skips no day across window boundaries for an adapter that treats after as exclusive" do
          exclusive_klass = Class.new(Adapter) do
            class << self
              def backfill_range = 30
            end

            define_method(:fetch) do |after: nil, upto: nil|
              ((after + 1)..(upto || Date.today)).map { |date| { date:, base: "EUR", quote: "USD", rate: 1.1 } }
            end
          end

          start = Date.today - 100
          dates = []
          exclusive_klass.fetch_each(after: start) { |records| dates.concat(records.map { |r| r[:date] }) }

          _(((start + 1)..Date.today).to_a - dates).must_be_empty
        end

        it "steps a one-day window a day at a time" do
          afters = []
          daily_klass = Class.new(Adapter) do
            class << self
              def backfill_range = 1
            end

            define_method(:fetch) do |after: nil, **|
              afters << after
              []
            end
          end

          daily_klass.fetch_each(after: Date.today - 3) { nil }

          _(afters).must_equal(((Date.today - 3)..Date.today).to_a)
        end
      end

      describe "#midpoint" do
        let(:adapter) { Class.new(Adapter).new }

        it "returns the exact mid of two published prices" do
          _(adapter.send(:midpoint, 181.5264, 181.76)).must_equal(181.6432)
        end

        it "carries at most one decimal beyond its inputs" do
          _(adapter.send(:midpoint, 1.1, 1.3)).must_equal(1.2)
          _(adapter.send(:midpoint, 0.0001234, 0.0001236)).must_equal(0.0001235)
        end

        it "returns the price itself when both sides agree" do
          _(adapter.send(:midpoint, 3.14, 3.14)).must_equal(3.14)
        end

        it "accepts prices still in string form" do
          _(adapter.send(:midpoint, "181.5264", "181.76")).must_equal(181.6432)
        end
      end

      describe "http client" do
        let(:adapter) { Class.new(Provider::Adapters::Adapter).new }
        let(:client) { adapter.send(:http) }
        let(:url) { "https://example.test/rates" }

        after { WebMock.reset! }

        it "returns 2xx responses" do
          WebMock.stub_request(:get, url).to_return(status: 200, body: "ok")

          assert_equal("ok", client.get(url).to_s)
        end

        it "raises on 4xx" do
          WebMock.stub_request(:get, url).to_return(status: 403, body: "<html>blocked</html>")

          error = assert_raises(HTTP::StatusError) { client.get(url) }
          assert_equal(403, error.response.code)
        end

        it "raises on 5xx" do
          WebMock.stub_request(:get, url).to_return(status: 500)

          assert_raises(HTTP::StatusError) { client.get(url) }
        end

        it "raises on 3xx so moved endpoints fail loudly" do
          WebMock.stub_request(:get, url).to_return(status: 301, headers: { "Location" => "https://example.test/new" })

          assert_raises(HTTP::StatusError) { client.get(url) }
        end

        it "sends an Accept header, whose absence some WAFs read as a bot" do
          stub = WebMock.stub_request(:get, url)
            .with(headers: { "Accept" => "*/*" })
            .to_return(status: 200, body: "ok")

          client.get(url).to_s

          WebMock.assert_requested(stub)
        end

        it "identifies as Frankfurter" do
          user_agent = "Mozilla/5.0 (compatible; Frankfurter; +https://github.com/lineofflight/frankfurter)"
          stub = WebMock.stub_request(:get, url)
            .with(headers: { "User-Agent" => user_agent })
            .to_return(status: 200, body: "ok")

          client.get(url).to_s

          WebMock.assert_requested(stub)
        end

        it "sends requests unsigned without a Web Bot Auth key" do
          signature_headers = ["Signature", "Signature-Input", "Signature-Agent"]
          WebMock.stub_request(:get, url).to_return(status: 200, body: "ok")

          client.get(url).to_s

          WebMock.assert_requested(:get, url) do |request|
            signature_headers.none? { |name| request.headers.key?(name) }
          end
        end

        it "links to the requests page and signs each request once there is a Web Bot Auth key" do
          key = turn_on_web_bot_auth
          signed = []
          WebMock.stub_request(:get, %r{\Ahttps://example\.test:8443/}).to_return do |request|
            signed << request.headers
            { status: 200, body: "ok" }
          end

          client.get("https://Example.Test:8443/rates").to_s
          client.get("https://Example.Test:8443/more").to_s

          assert_equal(2, signed.size)
          signed.each do |headers|
            assert_equal("Mozilla/5.0 (compatible; Frankfurter; +https://frankfurter.dev/requests)", headers["User-Agent"])
            assert_equal('"https://api.frankfurter.dev"', headers["Signature-Agent"])
            assert_signed(
              headers,
              { '"@authority"' => "example.test:8443", '"signature-agent"' => '"https://api.frankfurter.dev"' },
              key,
            )
          end
          refute_equal(signed.first["Signature"], signed.last["Signature"])
        end

        it "lets a self-hosted instance sign with its own authority without claiming our requests page" do
          key = turn_on_web_bot_auth
          WebBotAuth.authority = "fx.example.test"
          headers = nil
          WebMock.stub_request(:get, url).to_return do |request|
            headers = request.headers
            { status: 200, body: "ok" }
          end

          client.get(url).to_s

          assert_equal(
            "Mozilla/5.0 (compatible; Frankfurter; +https://github.com/lineofflight/frankfurter)",
            headers["User-Agent"],
          )
          assert_equal('"https://fx.example.test"', headers["Signature-Agent"])
          assert_signed(
            headers,
            { '"@authority"' => "example.test", '"signature-agent"' => '"https://fx.example.test"' },
            key,
          )
        end

        describe "TLS chain completion" do
          let(:store) { OpenSSL::X509::Store.new }

          before { WebMock.disable_net_connect!(allow_localhost: true) }
          after { WebMock.disable_net_connect! }

          def issue_cert(common_name, issuer: nil, issuer_key: nil, ca_cert: false, ca_issuers_url: nil, san: nil)
            key = OpenSSL::PKey::EC.generate("prime256v1")
            cert = OpenSSL::X509::Certificate.new
            cert.version = 2
            cert.serial = Random.rand(1..1_000_000)
            cert.subject = OpenSSL::X509::Name.parse("/CN=#{common_name}")
            cert.issuer = issuer ? issuer.subject : cert.subject
            cert.public_key = key
            cert.not_before = Time.now - 60
            cert.not_after = Time.now + 3600

            ef = OpenSSL::X509::ExtensionFactory.new
            ef.subject_certificate = cert
            ef.issuer_certificate = issuer || cert
            cert.add_extension(ef.create_extension("basicConstraints", ca_cert ? "CA:TRUE" : "CA:FALSE", true))
            cert.add_extension(ef.create_extension("subjectKeyIdentifier", "hash", false))
            cert.add_extension(ef.create_extension("authorityKeyIdentifier", "keyid:always", false))
            cert.add_extension(ef.create_extension("subjectAltName", san, false)) if san
            if ca_issuers_url
              cert.add_extension(ef.create_extension("authorityInfoAccess", "CA Issuers;URI:#{ca_issuers_url}", false))
            end

            cert.sign(issuer_key || key, OpenSSL::Digest.new("SHA256"))
            [cert, key]
          end

          def with_tls_server(cert, key)
            tcp_server = TCPServer.new("127.0.0.1", 0)
            port = tcp_server.addr[1]
            ctx = OpenSSL::SSL::SSLContext.new
            ctx.cert = cert
            ctx.key = key
            ssl_server = OpenSSL::SSL::SSLServer.new(tcp_server, ctx)
            ssl_server.start_immediately = true

            thread = Thread.new do
              loop do
                client_sock = ssl_server.accept
                begin
                  if client_sock.gets
                    client_sock.write("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
                  end
                rescue OpenSSL::SSL::SSLError, IOError, SystemCallError
                  # Expected on the initial failed handshake and the leaf-inspection probe.
                ensure
                  client_sock&.close
                end
              rescue IOError, OpenSSL::SSL::SSLError, SystemCallError
                break if tcp_server.closed?
              end
            end

            VCR.turned_off do
              yield "https://127.0.0.1:#{port}/rates"
            end
          ensure
            thread&.kill
            tcp_server&.close
          end

          it "fetches a missing intermediate from CA Issuers, verifies it against the root store, and retries" do
            root, root_key = issue_cert("Root CA", ca_cert: true)
            intermediate, inter_key = issue_cert("Intermediate CA", issuer: root, issuer_key: root_key, ca_cert: true)
            leaf, leaf_key = issue_cert(
              "127.0.0.1",
              issuer: intermediate,
              issuer_key: inter_key,
              ca_issuers_url: "http://ca.example.test/intermediate.cer",
              san: "IP:127.0.0.1",
            )
            store.add_cert(root)

            WebMock.stub_request(:get, "http://ca.example.test/intermediate.cer")
              .to_return(status: 200, body: intermediate.to_der)

            with_tls_server(leaf, leaf_key) do |server_url|
              chasing_client = HTTP.use(complete_chain: { store: })
              response = chasing_client.get(server_url, ssl: { cert_store: store })

              assert_equal("ok", response.to_s)
              assert(store.verify(leaf))
            end
          end

          it "walks multiple missing intermediates up to a trusted root" do
            root, root_key = issue_cert("Root CA", ca_cert: true)
            inter1, inter1_key = issue_cert("Intermediate 1", issuer: root, issuer_key: root_key, ca_cert: true)
            inter2, inter2_key = issue_cert(
              "Intermediate 2",
              issuer: inter1,
              issuer_key: inter1_key,
              ca_cert: true,
              ca_issuers_url: "http://ca.example.test/inter1.cer",
            )
            leaf, leaf_key = issue_cert(
              "127.0.0.1",
              issuer: inter2,
              issuer_key: inter2_key,
              ca_issuers_url: "http://ca.example.test/inter2.cer",
              san: "IP:127.0.0.1",
            )
            store.add_cert(root)

            WebMock.stub_request(:get, "http://ca.example.test/inter2.cer")
              .to_return(status: 200, body: inter2.to_der)
            WebMock.stub_request(:get, "http://ca.example.test/inter1.cer")
              .to_return(status: 200, body: inter1.to_der)

            with_tls_server(leaf, leaf_key) do |server_url|
              chasing_client = HTTP.use(complete_chain: { store: })
              response = chasing_client.get(server_url, ssl: { cert_store: store })

              assert_equal("ok", response.to_s)
              assert(store.verify(leaf))
            end
          end

          it "rejects an intermediate that does not chain to a trusted root" do
            untrusted_root, untrusted_key = issue_cert("Untrusted Root", ca_cert: true)
            intermediate, inter_key = issue_cert(
              "Untrusted Intermediate",
              issuer: untrusted_root,
              issuer_key: untrusted_key,
              ca_cert: true,
            )
            leaf, leaf_key = issue_cert(
              "127.0.0.1",
              issuer: intermediate,
              issuer_key: inter_key,
              ca_issuers_url: "http://ca.example.test/intermediate.cer",
              san: "IP:127.0.0.1",
            )

            WebMock.stub_request(:get, "http://ca.example.test/intermediate.cer")
              .to_return(status: 200, body: intermediate.to_der)

            with_tls_server(leaf, leaf_key) do |server_url|
              chasing_client = HTTP.use(complete_chain: { store: })

              assert_raises(OpenSSL::SSL::SSLError) do
                chasing_client.get(server_url, ssl: { cert_store: store })
              end
              refute(store.verify(leaf))
            end
          end

          it "extracts only the verified intermediate from a multi-certificate PKCS#7 bundle" do
            root, root_key = issue_cert("Root CA", ca_cert: true)
            intermediate, inter_key = issue_cert("Intermediate CA", issuer: root, issuer_key: root_key, ca_cert: true)
            rogue_ca, rogue_key = issue_cert("Rogue CA", ca_cert: true)
            leaf, leaf_key = issue_cert(
              "127.0.0.1",
              issuer: intermediate,
              issuer_key: inter_key,
              ca_issuers_url: "http://ca.example.test/bundle.p7c",
              san: "IP:127.0.0.1",
            )
            store.add_cert(root)

            p7 = OpenSSL::PKCS7.sign(rogue_ca, rogue_key, "", [intermediate], OpenSSL::PKCS7::NOCERTS)
            p7.certificates = [rogue_ca, intermediate]

            WebMock.stub_request(:get, "http://ca.example.test/bundle.p7c")
              .to_return(status: 200, body: p7.to_der)

            with_tls_server(leaf, leaf_key) do |server_url|
              chasing_client = HTTP.use(complete_chain: { store: })
              response = chasing_client.get(server_url, ssl: { cert_store: store })

              assert_equal("ok", response.to_s)
              assert(store.verify(leaf))
              refute(store.verify(rogue_ca))
            end
          end
        end
      end
    end
  end
end
