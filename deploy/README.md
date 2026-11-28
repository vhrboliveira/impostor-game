# Production deploy

One Ubuntu VPS runs everything in Docker: Traefik (TLS, reverse proxy), the web app (nginx serving
the static build), the API (Go) and PostgreSQL. Cloudflare sits in front of every request, and the
server accepts connections from Cloudflare only.

```
player ──HTTPS──▶ Cloudflare (proxied DNS, SSL "Full (strict)")
                     │  only Cloudflare IPs reach :443 (ufw), with Cloudflare's client
                     │  certificate (Authenticated Origin Pulls, mTLS)
                     ▼
              Traefik (host network, :443, Cloudflare Origin Certificate)
                 ├── /api/*, /ws/*  ──▶ api  172.30.10.10:8080 ──TLS──▶ postgres (no ports)
                 └── everything else ─▶ web  172.30.10.11:8080 (nginx, static files)
```

- **One origin** (`https://vhrbo.tech`): the app at `/`, the API at `/api` and `/ws`. No CORS is
  needed; `ALLOWED_ORIGINS=https://vhrbo.tech` limits who may open a WebSocket.
- **Builds on the VPS** from the two git repositories (`impostor-game` = this repo,
  `impostor-game-web`). Migrations and the catalog seed run on every deploy, before the API starts
  (both are idempotent).
- **No backups** of the database (decided: rooms live at most 24 hours; the catalog comes from the
  repo).

## How access is restricted

| Layer | What it does | Where |
| --- | --- | --- |
| ufw | tcp/443 only from Cloudflare's IP ranges; SSH rate-limited; everything else dropped | `scripts/cloudflare.sh` |
| Authenticated Origin Pulls | TLS handshake requires Cloudflare's client certificate: a machine that only spoofs or borrows a Cloudflare IP fails. It is Cloudflare's shared certificate, so it proves "Cloudflare's proxy", not "this zone" | `generated/traefik/dynamic/tls.yml` |
| Traefik `ipAllowList` | 403 for a connection that does not come from Cloudflare (a second line behind ufw) | `generated/traefik/dynamic/routes.yml` |
| `sniStrict` + `Host` rules | only `vhrbo.tech` is served; other names fail the handshake | `routes.yml`, `tls.yml` |
| Docker networks | Postgres publishes no port and sits on an internal network (`db`) shared only with the API and the jobs | `compose.yml` |

Traefik runs on the **host network** on purpose: ports published by Docker bypass ufw (Docker
writes its own iptables rules), while a process on the host network is filtered by ufw like any
other.

**Real player address.** The API rate-limits per client IP. Cloudflare sends it in
`X-Forwarded-For`; Traefik keeps that header only on connections from Cloudflare
(`forwardedHeaders.trustedIPs`), and the API trusts it only from Traefik and Cloudflare
(`TRUSTED_PROXIES` in `generated/api.env`). The rest of the chain is ignored, so a forged header
from a player has no effect.

**Tokens stay out of logs.** The WebSocket URL carries the session token (`?token=`): Traefik's
access log is off, nginx's too, and the API logs paths without query strings.

## Files

| Path | In git | What |
| --- | --- | --- |
| `compose.yml` | yes | the stack |
| `.env.example` → `.env` | example only | domain, web repo path, database user, name and password |
| `postgres/pg_hba.conf` | yes | network logins only over TLS with a password |
| `scripts/cloudflare.sh` | yes | Cloudflare IPs + origin-pull CA → Traefik config, `TRUSTED_PROXIES`, ufw rules (sudo; also from cron) |
| `scripts/postgres-certs.sh` | yes | private CA + server certificate for Postgres (sudo, once) |
| `scripts/deploy.sh` | yes | `git pull` both repos, build, `up -d` |
| `secrets/traefik/origin.{pem,key}` | **no** | Cloudflare Origin Certificate |
| `secrets/postgres/{ca.crt,server.crt,server.key}` | **no** | from `postgres-certs.sh` |
| `generated/` | **no** | from `cloudflare.sh` (do not edit) |

The web image is built from `../../web/Dockerfile` (nginx config in `web/nginx/`, with the headers
listed in the web README's "Hosting" section).

## 1. Cloudflare (dashboard)

Do this first; the order of the other steps assumes it.

1. **DNS** → add an `A` record (and `AAAA` if the VPS has IPv6) for `vhrbo.tech` pointing to the
   VPS, **Proxied** (orange cloud).
2. **SSL/TLS → Overview** → encryption mode **Full (strict)**.
3. **SSL/TLS → Origin Server → Create Certificate**:
   - "Generate private key and CSR with Cloudflare", key type ECC (or RSA);
   - hostnames `vhrbo.tech` and `*.vhrbo.tech`; validity 15 years; format PEM;
   - copy the **Origin Certificate** and the **Private Key** somewhere safe right away (the
     password manager works): the key is shown only once. They go to the VPS in step 5.
4. **SSL/TLS → Origin Server → Authenticated Origin Pulls** → **On**. (Until Traefik is up the
   site answers 52x anyway; once it is up, Traefik refuses connections without this certificate.)
5. **SSL/TLS → Edge Certificates** → **Always Use HTTPS** on; **Minimum TLS Version** 1.2.
6. **Network** → **WebSockets** on (the default).
7. Leave off anything that injects scripts into pages, which the app's Content Security Policy
   blocks: **Rocket Loader**, **Email Address Obfuscation**, automatic **Web Analytics**
   injection. Do not turn on **Under Attack mode** or **Bot Fight Mode**: their JavaScript
   challenges break the API calls.

> **HSTS covers every subdomain.** nginx sends `Strict-Transport-Security: max-age=31536000;
> includeSubDomains` on `vhrbo.tech`: once a browser sees it, every `*.vhrbo.tech` must serve valid
> HTTPS for a year. If some subdomain cannot, drop `includeSubDomains` from
> `web/nginx/security-headers.inc` before the first deploy.

## 2. VPS base setup (once)

As a sudo user (not root) over SSH with a key:

```bash
sudo apt-get update && sudo apt-get -y upgrade
sudo apt-get install -y unattended-upgrades git curl openssl ufw
```

SSH: keys only. In `/etc/ssh/sshd_config` (or a file in `/etc/ssh/sshd_config.d/`) set
`PasswordAuthentication no` and `PermitRootLogin no`, then `sudo systemctl reload ssh`. Keep the
current session open and check that a new one still logs in before closing it.

**Firewall.** Allow SSH **before** enabling ufw, or the session drops and the VPS is locked out
(change 22 if SSH listens elsewhere; with a fixed home IP, `sudo ufw allow from <your-ip> to any
port 22 proto tcp` is tighter than `limit`):

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw limit 22/tcp
sudo ufw enable
sudo ufw status verbose
```

Port 443 is opened later by `scripts/cloudflare.sh`, for Cloudflare's ranges only. Port 80 stays
closed: Cloudflare redirects HTTP to HTTPS at the edge and only talks HTTPS to the server.

**Docker Engine** (official repository):

```bash
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker "$USER"   # log out and back in afterwards
```

Being in the `docker` group is equivalent to root: keep that user's SSH key safe.

## 3. Code

```bash
sudo install -d -o "$USER" -g "$USER" /opt/impostor
cd /opt/impostor
git clone git@github.com:vhrboliveira/impostor-game.git server
git clone https://github.com/vhrboliveira/impostor-game-web.git web
```

For private repositories, add a read-only **deploy key** (GitHub → repo → Settings → Deploy keys)
for each one and clone over SSH. `deploy.sh` expects the web repo at `../../web` from this folder
(`WEB_DIR` in `.env` changes it).

`server/docker-compose.yml` is the development database: do not run it on the VPS.

## 4. Settings

```bash
cd /opt/impostor/server/deploy
cp .env.example .env
chmod 600 .env
nano .env
```

- `APP_DOMAIN=vhrbo.tech`
- `POSTGRES_PASSWORD`: from the password manager, **letters and digits only** (it is placed
  inside `DATABASE_URL`), 32 characters or more. Postgres stores it when the data volume is first
  created; changing it later is in "Operations".

## 5. Certificates

**Cloudflare Origin Certificate** (from step 1.3):

```bash
cd /opt/impostor/server/deploy
sudo install -d -m 700 secrets secrets/traefik
sudo nano secrets/traefik/origin.pem   # paste the Origin Certificate
sudo nano secrets/traefik/origin.key   # paste the Private Key
sudo chmod 600 secrets/traefik/origin.key
```

**Database TLS** (private CA; the API checks it with `sslmode=verify-full`):

```bash
sudo scripts/postgres-certs.sh
```

## 6. Cloudflare ranges, Traefik config, firewall

```bash
sudo scripts/cloudflare.sh
sudo ufw status numbered   # 443/tcp ALLOW from each Cloudflare range, tagged cloudflare-https
```

Weekly refresh (Cloudflare rarely changes its ranges; the script only restarts what changed, and
refuses an empty or odd download instead of closing the firewall):

```bash
sudo tee /etc/cron.d/impostor-cloudflare >/dev/null <<'EOF'
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
17 4 * * 1 root /opt/impostor/server/deploy/scripts/cloudflare.sh >>/var/log/impostor-cloudflare.log 2>&1
EOF
```

## 7. Deploy

```bash
scripts/deploy.sh             # git pull both repos, build, migrate, seed, start
scripts/deploy.sh --no-pull   # same, with the code already on disk
```

The first build takes a few minutes. At the end, `migrate` and `seed` show `Exited (0)` and the
other services are `Up` (`traefik` and `postgres` also `healthy`).

## 8. Check

From your computer:

```bash
curl -sI https://vhrbo.tech | grep -iE '^(HTTP|server|strict-transport|content-security)'
# HTTP/2 200, server: cloudflare, the HSTS and CSP headers

curl -s -X POST https://vhrbo.tech/api/room -H 'Content-Type: application/json' -d '{}'
# a JSON error from the API (it was reached)

curl -skI --max-time 5 --resolve vhrbo.tech:443:<VPS_IP> https://vhrbo.tech
# times out: the server cannot be reached without Cloudflare
```

On the VPS:

```bash
curl -skI --resolve vhrbo.tech:443:127.0.0.1 https://vhrbo.tech
# fails in the TLS handshake (certificate required): only Cloudflare holds the client certificate

sudo ss -tlnp   # besides 127.0.0.x: only sshd (22) and traefik (443)

cd /opt/impostor/server/deploy && docker compose logs --tail 20 api
# "client" in the request logs is the real public IP of whoever played, not a Cloudflare address
```

Then play a short match with two or three phones (including one on mobile data).

## Operations

Run from `/opt/impostor/server/deploy`.

| Task | Command |
| --- | --- |
| Update after a push | `scripts/deploy.sh` |
| Status | `docker compose ps -a` |
| Logs | `docker compose logs -f api` (also `web`, `traefik`, `postgres`) |
| Restart one service | `docker compose restart api` |
| Stop everything | `docker compose down` (keeps the database volume) |
| Migration status | `docker compose run --rm migrate status` |
| Roll back the last migration group | `docker compose run --rm migrate down` |
| Database shell | `docker compose exec postgres psql -U impostor -d impostor` |

A deploy restarts the API for a few seconds: open WebSockets close and the apps reconnect on their
own; rooms are in the database.

**Change the database password.** Inside the database shell run `\password impostor` (it prompts,
so the password stays out of the shell history), put the same value in `.env`, then
`docker compose up -d api`.

**Renew the database certificates** (valid 10 years):
`sudo scripts/postgres-certs.sh --force && docker compose up -d --force-recreate postgres api`.

**Replace the Origin Certificate** (valid 15 years): overwrite the two files in `secrets/traefik/`,
then `docker compose restart traefik`.

**Wipe the database** (all rooms and the catalog; the next deploy seeds it again):
`docker compose down -v`. This cannot be undone.

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| Cloudflare 521 | Traefik is down (`docker compose ps`), or ufw lacks the Cloudflare rules (`sudo scripts/cloudflare.sh`). |
| Cloudflare 525 / 526 | SSL mode is not "Full (strict)", the origin certificate files are wrong, or Authenticated Origin Pulls is off while Traefik requires it (step 1.4). |
| 403 from Traefik | The connection did not come from a Cloudflare address: the record is not proxied, or the ranges are stale (`sudo scripts/cloudflare.sh`). |
| 503 on `/api` | The API failed its readiness check: `docker compose logs api` (usually the database). |
| WebSocket closes at once | `ALLOWED_ORIGINS` does not match the address in the browser (`APP_DOMAIN` in `.env`). |
| Every player shows the same IP / rate limits hit for everyone | `generated/api.env` is missing Cloudflare's ranges, or the edge network's gateway is not `172.30.10.1`. |
| API exits with a database TLS error | The certificate does not name `postgres` or the CA does not match: `sudo scripts/postgres-certs.sh --force` and recreate `postgres` and `api`. |
| `172.30.10.0/24` clashes with another network on the VPS | Change the subnet, gateway and the two fixed addresses in `compose.yml` together with the constants at the top of `scripts/cloudflare.sh`. |
