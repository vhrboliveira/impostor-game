#!/usr/bin/env bash
# Updates both repositories, rebuilds the images and restarts what changed. Migrations and the
# catalog seed run before the API starts. Run as the deploy user (member of the docker group).
set -euo pipefail

cd "$(dirname "$0")/.."

die() { echo "deploy.sh: $*" >&2; exit 1; }

[[ -f .env ]] || die ".env is missing: copy .env.example"
[[ -f generated/api.env ]] || die "generated/ is missing: run sudo scripts/cloudflare.sh first"
[[ -f secrets/postgres/server.crt ]] || die "database certificates are missing: run sudo scripts/postgres-certs.sh"

web_dir=$(sed -n 's/^WEB_DIR=//p' .env | tail -n 1 | tr -d '"'"'")
web_dir=${web_dir:-../../web}
[[ -d $web_dir/.git ]] || die "web repository not found at $web_dir (WEB_DIR in .env)"

if [[ ${1:-} != "--no-pull" ]]; then
  git -C .. pull --ff-only
  git -C "$web_dir" pull --ff-only
fi

docker compose build
docker compose up -d --remove-orphans
docker image prune -f >/dev/null

docker compose ps -a
