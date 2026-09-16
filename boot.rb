# frozen_string_literal: true

require "openssl"

$LOAD_PATH << File.expand_path("lib", __dir__)
require "currency_patches"

# Some provider sites serve their leaf certificate without the intermediate that issued it, so OpenSSL can't build a
# chain to a system root. Browsers fetch the missing link on the fly; OpenSSL doesn't. Add those intermediates to Ruby's
# process-wide default store, which every SSLContext without an explicit store (http.rb's included) falls back to.
# Chains still have to end at a system root. See config/ca_bundles/README.md.
Dir[File.expand_path("config/ca_bundles/*.pem", __dir__)].each do |pem|
  OpenSSL::SSL::SSLContext::DEFAULT_CERT_STORE.add_file(pem)
end
