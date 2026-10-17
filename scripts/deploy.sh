#!/bin/sh
# Deploys Talespeak to one server over SSH, from CI or by hand:
# copies compose.yml and Caddyfile, writes .env from deploy/env/<env>.env,
# pulls and starts the image, then checks https://<domain>/healthz until it
# reports the expected version.
#
# Usage: scripts/deploy.sh <env> <image-tag> <expected-version>
# Environment: DEPLOY_SSH_TARGET (user@host), GH_OWNER, GHCR_USER, GHCR_TOKEN.
# SSH key and known_hosts must already be set up.
set -eu
env=$1 tag=$2 want=$3
settings=deploy/env/$env.env
[ -f "$settings" ] || { echo "deploy: no $settings" >&2; exit 1; }

domain=$(sed -n 's/^TALESPEAK_DOMAIN=//p' "$settings" | tail -n 1)
[ -n "$domain" ] || { echo "deploy: TALESPEAK_DOMAIN is empty in $settings" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp deploy/compose.yml deploy/Caddyfile deploy/remote-up.sh "$work/"
{
  echo "# Written by scripts/deploy.sh from $settings. Changes here are overwritten on the next deploy."
  grep -v '^[[:space:]]*#' "$settings" | grep -v '^[[:space:]]*$'
  echo "GH_OWNER=$GH_OWNER"
  echo "TALESPEAK_VERSION=$tag"
} > "$work/.env"

echo "deploy: $env, image tag $tag, https://$domain"
ssh "$DEPLOY_SSH_TARGET" 'mkdir -p ~/talespeak && chmod 700 ~/talespeak'
scp -q "$work/compose.yml" "$work/Caddyfile" "$work/remote-up.sh" "$work/.env" "$DEPLOY_SSH_TARGET:talespeak/"
printf '%s\n' "$GHCR_TOKEN" | ssh "$DEPLOY_SSH_TARGET" "chmod 600 ~/talespeak/.env && sh ~/talespeak/remote-up.sh '$GHCR_USER'"

# From the outside: DNS, Caddy, certificate and the new version all have to work.
i=0
while [ $i -lt 36 ]; do
  body=$(curl -fsS --max-time 5 "https://$domain/healthz" 2>/dev/null || true)
  case $body in
    *"\"version\":\"$want\""*) echo "deploy: healthy, $body"; exit 0 ;;
  esac
  i=$((i + 1))
  sleep 5
done
echo "deploy: https://$domain/healthz did not report version $want within 3 minutes (last: ${body:-no answer})" >&2
exit 1
