#!/usr/bin/env bash
# Checks the Linux release archive on a machine with systemd, as root: installs
# it, checks the service answers with the archive's version, updates it and
# checks the settings were kept, then uninstalls it.
#
#   sudo linux/check-install.sh usage-control-1.2.3-linux-amd64.tar.gz 1.2.3
set -euo pipefail

archive="$(realpath "$1")"
version="$2"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
tar -xzf "$archive" -C "$work"
folder="$(find "$work" -mindepth 1 -maxdepth 1 -type d)"

# Waits until the service answers at path and prints the answer.
answer() {
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:8080$1" 2> /dev/null; then
      return 0
    fi
    sleep 1
  done
  journalctl -u usage-control --no-pager | tail -40 >&2
  echo "usage-control did not answer $1" >&2
  return 1
}

"$folder/install.sh"
answer /api/metrics | grep -q "\"version\":\"$version\"" || { echo "the service does not report version $version" >&2; exit 1; }
systemctl is-enabled --quiet usage-control

# An update keeps the settings and the history.
sed -i 's/^DEVICE_NAME=.*/DEVICE_NAME=Install check/' /etc/usage-control.env
"$folder/install.sh"
answer /api/devices | grep -q '"name":"Install check"' || { echo "the update lost DEVICE_NAME" >&2; exit 1; }
test -f /var/lib/usage-control/usage-control.db

"$folder/install.sh" --uninstall --purge
if systemctl cat usage-control > /dev/null 2>&1 || [[ -e /usr/local/bin/usage-control || -e /etc/usage-control.env ]]; then
  echo "the uninstall left usage-control behind" >&2
  exit 1
fi
echo "Install, update and uninstall work."
