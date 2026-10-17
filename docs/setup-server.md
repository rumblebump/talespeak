# Server setup guide

Run your own server with Docker, Caddy and automatic HTTPS from Let's Encrypt. Takes about 15 minutes on a fresh Linux machine.

## What you need

| | Minimum | With the AI sidecar |
| --- | --- | --- |
| CPU | 1 vCPU, x86-64 or ARM64 (Raspberry Pi 4 or newer works) | 2–4 vCPU |
| RAM | 1 GB | 4 GB |
| Disk | 2 GB | 6 GB (models) |
| GPU | Not needed | Not needed |

You also need:

- A domain or subdomain (for example `talespeak.example.org`) with an **A record** (and AAAA for IPv6) pointing to the server.
- Ports **80** and **443** open to the internet. Let's Encrypt needs port 80 to issue the certificate.
- Docker Engine with the Compose plugin.

These figures are targets from the design and will be updated once measured.

## 1. Install Docker

On Debian or Ubuntu:

```sh
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"   # log out and back in afterwards
docker compose version
```

## 2. Get the deploy files

```sh
mkdir -p ~/talespeak && cd ~/talespeak
base=https://raw.githubusercontent.com/OWNER/talespeak/main/deploy
curl -fsSLO "$base/compose.yml"
curl -fsSLO "$base/Caddyfile"
curl -fsSL "$base/.env.example" -o .env
```

## 3. Configure

Edit `.env`:

```sh
TALESPEAK_DOMAIN=talespeak.example.org
GH_OWNER=OWNER
TALESPEAK_VERSION=0.1.0              # pin a release; "latest" also works
TALESPEAK_KIDS_MODE=false            # true on servers where kids chat
```

`.env` is the only file with your settings. Keep it private. Email is optional; to turn it on, see [admin-email.md](admin-email.md).

## 4. Start

```sh
docker compose up -d
docker compose logs -f caddy   # watch for "certificate obtained successfully"
```

Open `https://talespeak.example.org`. The first account created becomes the admin.

## 5. Optional: AI sidecar

Only needed for NPC small talk and story flavor. The game works fully without it.

```sh
mkdir -p models
# download a GGUF model into ./models, for example LFM2.5-1.2B-Instruct Q4_K_M
```

Then set in `.env`:

```sh
TALESPEAK_LLM_URL=http://llm:8081
LLM_MODEL_FILE=LFM2.5-1.2B-Instruct-Q4_K_M.gguf
LLM_THREADS=2
```

and start with the profile:

```sh
docker compose --profile ai up -d
```

## Updating

```sh
cd ~/talespeak
# change TALESPEAK_VERSION in .env, then
docker compose pull && docker compose up -d
docker image prune -f
```

## Backups

Everything the game stores is in the `talespeak_data` volume (one SQLite database plus uploads). Certificates are in `caddy_data`.

```sh
docker compose stop talespeak
docker run --rm -v talespeak_talespeak_data:/data -v "$PWD":/backup alpine \
  tar czf /backup/talespeak-data-$(date +%F).tar.gz -C /data .
docker compose start talespeak
```

The volume name is `<folder>_talespeak_data`, so `talespeak_talespeak_data` when the folder is `~/talespeak`. An online `talespeak backup` command that needs no downtime is planned.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| No certificate | DNS points to this server (`dig +short talespeak.example.org`), port 80 reachable from outside |
| 502 from Caddy | `docker compose logs talespeak` |
| AI features silent | `TALESPEAK_LLM_URL` set, `docker compose --profile ai ps` shows `llm` running |
