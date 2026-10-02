#!/usr/bin/env bash
# deploy.sh - deploy usage-control to a Raspberry Pi over SSH.
#
# Copies compose.yaml and .env to the Pi, pulls the published image and
# (re)starts the container with Docker Compose. Nothing is built on the Pi.
#
# Needs on your PC: ssh and scp, with key login to the Pi.
# Needs on the Pi:  Docker with the compose plugin, and the SSH user in the
#                   "docker" group (rpi-setup's docker task sets both up).
#
# Settings come from .env next to compose.yaml (see .env.example); a variable
# set on the command line wins:
#   PI_HOST=pi5.local bash scripts/deploy.sh
#
# Run: bash scripts/deploy.sh [--dry-run]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/.env"

usage() { sed -n '2,17s/^# \{0,1\}//p' "${BASH_SOURCE[0]}"; }
die() { printf '[FAIL] %s\n' "$*" >&2; exit 1; }
step() { printf '==> %s\n' "$*"; }

dry_run=false
case "${1:-}" in
  '') ;;
  --dry-run) dry_run=true ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; die "unknown option: $1" ;;
esac

[[ -f "$ENV_FILE" ]] || die "$ENV_FILE not found; copy .env.example to .env and fill in PI_HOST"

# Load .env without overriding variables given on the command line.
while IFS= read -r line || [[ -n "$line" ]]; do
  [[ "$line" =~ ^[[:space:]]*(#|$) ]] && continue
  [[ "$line" =~ ^([A-Z_][A-Z0-9_]*)=(.*)$ ]] || die ".env: cannot read line: $line"
  name="${BASH_REMATCH[1]}" value="${BASH_REMATCH[2]}"
  [[ "$value" =~ ^\"(.*)\"$ || "$value" =~ ^\'(.*)\'$ ]] && value="${BASH_REMATCH[1]}"
  [[ -n "${!name+set}" ]] || printf -v "$name" '%s' "$value"
done < "$ENV_FILE"

PI_HOST="${PI_HOST:-}"
PI_USER="${PI_USER:-}"
PI_DIR="${PI_DIR:-usage-control}"
[[ -n "$PI_HOST" ]] || die "PI_HOST is empty; set it in .env, e.g. PI_HOST=pi5.local"
[[ "$PI_DIR" != /* && "$PI_DIR" != *..* ]] || die "PI_DIR must be a folder below the home directory, got: $PI_DIR"

target="${PI_USER:+$PI_USER@}$PI_HOST"
remote_dir="$(printf '%q' "$PI_DIR")"

run() {
  if $dry_run; then printf '[dry-run] %s\n' "$*"; else "$@"; fi
}
remote() { run ssh -o BatchMode=yes "$target" "$1"; }

step "Checking $target"
# shellcheck disable=SC2016 # $(id -un) runs on the Pi
remote 'docker compose version >/dev/null 2>&1 || { echo "Docker with the compose plugin is missing, or $(id -un) may not use it (add the user to the docker group)" >&2; exit 1; }' \
  || die "cannot use Docker on $target over SSH (key login and Docker are needed)"

step "Copying compose.yaml and .env to $target:$PI_DIR"
# The settings file is read by Docker Compose on the Pi; keep it private there
# (scp keeps the mode of a file that already exists).
remote "mkdir -p $remote_dir && touch $remote_dir/.env && chmod 600 $remote_dir/.env"
run scp -q -o BatchMode=yes "$ROOT/compose.yaml" "$target:$PI_DIR/compose.yaml"
run scp -q -o BatchMode=yes "$ENV_FILE" "$target:$PI_DIR/.env"

# Variables given on the command line win over .env on the Pi too.
compose_env=""
for name in PORT IMAGE_TAG; do
  [[ -n "${!name:-}" ]] && compose_env+="$name=$(printf '%q' "${!name}") "
done

# --no-build: compose.yaml can also build from a checkout, which the Pi does not have.
step "Pulling the image (tag ${IMAGE_TAG:-main}) and starting usage-control"
remote "cd $remote_dir && ${compose_env}docker compose pull && ${compose_env}docker compose up -d --no-build --remove-orphans && ${compose_env}docker compose ps"

step "Deployed to $target (port ${PORT:-8080})"
