#!/usr/bin/env bash
# Creates the TLS certificate of the database (secrets/postgres/), signed by a private CA that the
# api, migrate and seed containers check (sslmode=verify-full). Run once with sudo; --force
# replaces existing certificates (then: docker compose up -d --force-recreate postgres api).
#
# The CA key is deleted after signing: renewing means running this again with --force.
set -euo pipefail

cd "$(dirname "$0")/.."

die() { echo "postgres-certs.sh: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "run with sudo (the key must belong to the postgres user of the image)"
command -v openssl >/dev/null || die "openssl is missing"

dir=secrets/postgres
if [[ -e $dir/server.crt && ${1:-} != "--force" ]]; then
  die "$dir already has certificates (use --force to replace them)"
fi

# uid/gid of the postgres user in the postgres:*-alpine image.
readonly pg_uid=70
readonly days=3650

umask 077
mkdir -p "$dir"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days "$days" \
  -subj "/CN=impostor postgres CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -keyout "$work/ca.key" -out "$work/ca.crt" 2>/dev/null

# The API connects to the host "postgres" (the compose service name).
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -subj "/CN=postgres" -keyout "$work/server.key" -out "$work/server.csr" 2>/dev/null
cat >"$work/server.ext" <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=DNS:postgres
EXT
openssl x509 -req -in "$work/server.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" \
  -set_serial "0x$(openssl rand -hex 16)" -days "$days" -extfile "$work/server.ext" \
  -out "$work/server.crt" 2>/dev/null

openssl verify -CAfile "$work/ca.crt" "$work/server.crt" >/dev/null

# Readable by the api image's nonroot user; the key only by postgres.
install -m 644 "$work/ca.crt" "$dir/ca.crt"
install -m 644 -o "$pg_uid" -g "$pg_uid" "$work/server.crt" "$dir/server.crt"
install -m 600 -o "$pg_uid" -g "$pg_uid" "$work/server.key" "$dir/server.key"

echo "postgres-certs.sh: created $dir/{ca.crt,server.crt,server.key}, valid for $days days"
