#!/usr/bin/env bash
# check-no-secrets.sh - fail when a commit would publish settings or secrets.
#
# Checks every file git tracks in DIR (default: this checkout):
#   - no .env settings file besides .env.example
#   - no file of router passwords (router-passwords, *.cred)
#   - no line starting with "-----BEGIN ... PRIVATE KEY-----"
#   - no line in a tracked .env file or example, such as
#     linux/usage-control.env.example, giving a *PASSWORD, *KEY, *TOKEN or
#     *SECRET a value
#
# Run: bash ci/check-no-secrets.sh [DIR]
set -euo pipefail

DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$DIR"
problems=0
problem() { printf '[FAIL] %s\n' "$*" >&2; problems=$((problems + 1)); }

while IFS= read -r f; do
  case "${f##*/}" in
    .env.example) ;;
    .env|.env.*|*.env) problem "$f is a settings file; keep filled settings out of the repository" ;;
    router-passwords|*.cred) problem "$f holds router passwords; keep it out of the repository" ;;
  esac
done < <(git ls-files)

while IFS= read -r hit; do
  problem "${hit%%:*}: holds a private key"
done < <(git grep -lE -e '^-----BEGIN ([A-Z]+ )*PRIVATE KEY-----' || true)

while IFS= read -r hit; do
  problem "$hit: a password or key has a value in a committed settings file"
done < <(git grep -nE -e '^[[:space:]]*(export[[:space:]]+)?[A-Z0-9_]*(PASSWORD|KEY|TOKEN|SECRET)=[[:space:]]*[^[:space:]#]' \
           -- '.env*' '*/.env*' '*.env' '*.env.*' | cut -d: -f1,2 || true)

if [[ $problems -gt 0 ]]; then
  echo "Found $problems possible secret(s). Keep settings in a git-ignored .env instead." >&2
  exit 1
fi
echo '[PASS] No settings files, private keys or passwords are tracked.'
