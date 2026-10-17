#!/bin/sh
# Runs on the server in ~/talespeak, started by scripts/deploy.sh over SSH.
# Reads a short-lived registry token on stdin, pulls the images, starts them
# and waits until every container is running and healthy.
# Usage: sh remote-up.sh <registry-user> < token
set -eu
cd "$(dirname "$0")"

docker compose config --quiet
docker login ghcr.io -u "$1" --password-stdin >/dev/null
trap 'docker logout ghcr.io >/dev/null 2>&1 || true' EXIT
docker compose pull --quiet
docker compose up -d --remove-orphans --wait --wait-timeout 120
docker compose ps
docker image prune -f >/dev/null
