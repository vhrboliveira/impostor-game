#!/usr/bin/env bash
# Keeps the server reachable only through Cloudflare. Run with sudo; safe to run again (cron).
#
# 1. Downloads Cloudflare's IP ranges and the Authenticated Origin Pulls CA.
# 2. Renders, under generated/:
#    - traefik/traefik.yml        static config (X-Forwarded-For trusted only from Cloudflare)
#    - traefik/dynamic/*.yml      routes, Cloudflare IP allow list, TLS (origin cert + mTLS)
#    - api.env                    TRUSTED_PROXIES of the API (Traefik + Cloudflare)
# 3. Allows tcp/443 in ufw only from those ranges (rules tagged "cloudflare-https").
# 4. Restarts Traefik / recreates the API when their config changed and they are running.
set -euo pipefail
shopt -s inherit_errexit

cd "$(dirname "$0")/.."
deploy_dir=$(pwd)

die() { echo "cloudflare.sh: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "run with sudo (it changes ufw rules)"
command -v curl >/dev/null || die "curl is missing"
command -v openssl >/dev/null || die "openssl is missing"
command -v ufw >/dev/null || die "ufw is missing"
[[ $(ufw status) == "Status: active"* ]] || die "ufw is inactive: do the firewall step of README.md first"

# Gateway of the "edge" network in compose.yml: where Traefik's requests reach the API from.
readonly edge_gateway=172.30.10.1
readonly api_addr=172.30.10.10
readonly web_addr=172.30.10.11
readonly tag=cloudflare-https

# APP_DOMAIN from .env, without sourcing it (it also holds the database password).
[[ -f .env ]] || die ".env is missing: copy .env.example"
domain=$(sed -n 's/^APP_DOMAIN=//p' .env | tail -n 1 | tr -d '[:space:]"'"'")
[[ $domain =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || die "APP_DOMAIN in .env is not a host name: '$domain'"

[[ -s secrets/traefik/origin.pem && -s secrets/traefik/origin.key ]] ||
  die "secrets/traefik/origin.pem and origin.key are missing (Cloudflare Origin Certificate, see README.md)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fetch() { curl -fsS --retry 3 --max-time 30 "$1"; }

# One CIDR per line; a missing trailing newline is fine. Refuses anything odd rather than
# opening the firewall to it.
ranges() {
  local url=$1 pattern=$2 min=$3 list
  list=$(fetch "$url" | tr -d '\r' | sed '/^[[:space:]]*$/d') || die "cannot download $url"
  while read -r cidr; do
    [[ $cidr =~ $pattern ]] || die "unexpected line in $url: '$cidr'"
  done <<<"$list"
  (($(wc -l <<<"$list") >= min)) || die "too few ranges in $url"
  echo "$list"
}

v4=$(ranges https://www.cloudflare.com/ips-v4/ '^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$' 10)
v6=$(ranges https://www.cloudflare.com/ips-v6/ '^[0-9a-fA-F:]+/[0-9]{1,3}$' 5)
all=$(printf '%s\n%s\n' "$v4" "$v6")

fetch https://developers.cloudflare.com/ssl/static/authenticated_origin_pull_ca.pem >"$tmp/ca.pem" ||
  die "cannot download the Authenticated Origin Pulls CA"
openssl x509 -in "$tmp/ca.pem" -noout -checkend 2592000 >/dev/null ||
  die "the Authenticated Origin Pulls CA is invalid or expires within 30 days"

yaml_list() { # indent, then one "- value" per range
  local indent=$1
  while read -r cidr; do printf '%s- "%s"\n' "$indent" "$cidr"; done <<<"$all"
}

mkdir -p "$tmp/traefik/dynamic"

cat >"$tmp/traefik/traefik.yml" <<EOF
# Rendered by scripts/cloudflare.sh: do not edit.
global:
  checkNewVersion: false
  sendAnonymousUsage: false
  # Drops headers whose names alias another (X_Forwarded_For for X-Forwarded-For).
  aliasHeadersStrategy: delete

log:
  level: INFO
# No access log: the WebSocket URL carries the session token (?token=). The API logs every
# request without its query string.

entryPoints:
  websecure:
    address: ":443"
    http:
      tls: {}
      middlewares:
        - cloudflare-only@file
    # Keeps Cloudflare's X-Forwarded-For (the player's address) only on requests from Cloudflare.
    forwardedHeaders:
      trustedIPs:
$(yaml_list "        ")
  # Health check of the container only (traefik healthcheck --ping).
  ping:
    address: "127.0.0.1:8082"

ping:
  entryPoint: ping

providers:
  file:
    directory: /etc/traefik/dynamic
    watch: true
EOF

cat >"$tmp/traefik/dynamic/routes.yml" <<EOF
# Rendered by scripts/cloudflare.sh: do not edit.
http:
  middlewares:
    # Second line of defence behind ufw: the connection itself must come from Cloudflare.
    cloudflare-only:
      ipAllowList:
        sourceRange:
$(yaml_list "          ")

  routers:
    api:
      rule: "Host(\`$domain\`) && (PathPrefix(\`/api/\`) || PathPrefix(\`/ws/\`))"
      entryPoints: [websecure]
      service: api
    web:
      rule: "Host(\`$domain\`)"
      entryPoints: [websecure]
      service: web

  services:
    api:
      loadBalancer:
        servers:
          - url: "http://$api_addr:8080"
        healthCheck:
          path: /readyz
          interval: 10s
          timeout: 3s
    web:
      loadBalancer:
        servers:
          - url: "http://$web_addr:8080"
EOF

cat >"$tmp/traefik/dynamic/tls.yml" <<'EOF'
# Rendered by scripts/cloudflare.sh: do not edit.
tls:
  # Listed here so sniStrict matches it (the default certificate does not count for sniStrict);
  # also the default, so Traefik never serves its own self-signed one.
  certificates:
    - certFile: /etc/traefik/origin/origin.pem
      keyFile: /etc/traefik/origin/origin.key
  stores:
    default:
      defaultCertificate:
        certFile: /etc/traefik/origin/origin.pem
        keyFile: /etc/traefik/origin/origin.key
  options:
    default:
      minVersion: VersionTLS12
      sniStrict: true
      # Authenticated Origin Pulls: only Cloudflare holds a client certificate signed by this CA.
      clientAuth:
        caFiles:
          - /etc/traefik/cloudflare-origin-pull-ca.pem
        clientAuthType: RequireAndVerifyClientCert
EOF

cp "$tmp/ca.pem" "$tmp/traefik/cloudflare-origin-pull-ca.pem"

{
  echo "# Rendered by scripts/cloudflare.sh: do not edit."
  printf 'TRUSTED_PROXIES=%s' "$edge_gateway"
  while read -r cidr; do printf ',%s' "$cidr"; done <<<"$all"
  echo
} >"$tmp/api.env"

# Installs a rendered file when it differs; prints its name when it changed.
install_file() {
  local rel=$1 mode=$2
  local src="$tmp/$rel" dst="generated/$rel"
  if [[ -f $dst ]] && cmp -s "$src" "$dst"; then return; fi
  mkdir -p "$(dirname "$dst")"
  install -m "$mode" "$src" "$dst"
  echo "$rel"
}

changed=$(
  install_file traefik/traefik.yml 644
  install_file traefik/dynamic/routes.yml 644
  install_file traefik/dynamic/tls.yml 644
  install_file traefik/cloudflare-origin-pull-ca.pem 644
  install_file api.env 644
)

# ufw: add the current ranges first, then drop the stale ones, so 443 never closes for Cloudflare.
while read -r cidr; do
  ufw allow proto tcp from "$cidr" to any port 443 comment "$tag" >/dev/null
done <<<"$all"
ufw status | awk -v tag="$tag" 'index($0, "# " tag) { for (i = 1; i <= NF; i++) if ($i == "#") print $(i - 1) }' |
  sort -u | while read -r cidr; do
    grep -qxF "$cidr" <<<"$all" || {
      ufw delete allow proto tcp from "$cidr" to any port 443 >/dev/null
      echo "ufw: removed stale range $cidr"
    }
  done

if [[ -z $changed ]]; then
  echo "cloudflare.sh: nothing changed"
  exit 0
fi
echo "cloudflare.sh: updated:"
sed 's/^/  generated\//' <<<"$changed"

running() { [[ -n $(docker compose --project-directory "$deploy_dir" ps --status running -q "$1" 2>/dev/null) ]]; }

# Routes and TLS files are reloaded by Traefik on their own; the static config, the CA (a file
# mount) and the API's environment need a restart.
if grep -qE '^traefik/(traefik\.yml|cloudflare-origin-pull-ca\.pem)$' <<<"$changed" && running traefik; then
  docker compose --project-directory "$deploy_dir" restart traefik
fi
if grep -qx 'api.env' <<<"$changed" && running api; then
  docker compose --project-directory "$deploy_dir" up -d --no-deps api
fi
