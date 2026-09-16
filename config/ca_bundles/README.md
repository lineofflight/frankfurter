# Bundled intermediates

Some provider sites serve their leaf certificate without the intermediate that issued it. Browsers fetch the missing link on the fly; OpenSSL does not, so every fetch fails with `certificate verify failed (unable to get local issuer certificate)`.

`boot.rb` adds every `.pem` in this folder to Ruby's default trust store. Chains still have to end at a system root, so nothing new is trusted; the store just has the missing link available.

To add one, read the CA Issuers URL off the site's leaf, fetch it, and confirm it completes the chain:

```sh
openssl s_client -connect HOST:443 -servername HOST </dev/null 2>/dev/null | openssl x509 -out leaf.pem
openssl x509 -in leaf.pem -noout -ext authorityInfoAccess
curl -o intermediate.der "<CA Issuers URL>"
openssl x509 -inform DER -in intermediate.der -out <certificate-name>.pem
openssl verify -untrusted <certificate-name>.pem leaf.pem   # must print OK
```

Name the file after the certificate, not the provider, and list the sites that need it in the header comment.
