#!/usr/bin/env bash
# Regenerate the byte-cluster dev TLS material (CA + leaf cert).
#
# All output files live next to this script and are gitignored — never
# commit the private keys.
#
# Run when:
#   - the leaf expires (validity 397 days — Apple's cap for trusted certs)
#   - you need to add a new SAN (edit openssl-leaf.cnf, then rerun)
#   - you rotated the CA on purpose (re-trust on Mac required)
#
# By default the CA is preserved if ca.crt + ca.key already exist so
# leaf rotations don't force every dev to re-trust on Mac. Pass --rotate-ca
# to force a fresh CA.
#
# After regen, push the new leaf into the cluster:
#   kubectl -n <ns> create secret tls aegis-edge-tls \
#     --cert=server.crt --key=server.key \
#     --dry-run=client -o yaml | kubectl apply -f -
#   kubectl -n <ns> rollout restart deployment <release>-edge-proxy

set -euo pipefail
cd "$(dirname "$0")"

ROTATE_CA=0
[[ "${1:-}" == "--rotate-ca" ]] && ROTATE_CA=1

if [[ $ROTATE_CA -eq 1 || ! -f ca.crt || ! -f ca.key ]]; then
  echo "Generating new CA..."
  openssl ecparam -name prime256v1 -genkey -noout -out ca.key
  openssl req -x509 -new -key ca.key -sha256 -days 3650 -out ca.crt \
    -subj "/CN=Aegis Dev Root CA/O=Aegis Lab" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign"
else
  echo "Reusing existing CA (pass --rotate-ca to regenerate)."
fi

echo "Generating leaf signed by CA..."
openssl ecparam -name prime256v1 -genkey -noout -out server.key
openssl req -new -key server.key -out server.csr -subj "/CN=aegis-byte-cluster"
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out server.crt -days 397 -sha256 \
  -extfile openssl-leaf.cnf -extensions v3_req
rm -f server.csr ca.srl

echo
echo "--- CA ---"
openssl x509 -in ca.crt -noout -subject -dates -ext basicConstraints
echo "--- Leaf ---"
openssl x509 -in server.crt -noout -subject -dates -ext subjectAltName
