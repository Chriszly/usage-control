#!/usr/bin/env bash
# Installs or updates usage-control as a systemd service, from the folder of an
# unpacked release archive (usage-control-<version>-linux-<arch>.tar.gz):
#
#   sudo ./install.sh              # install, or update to this version
#   sudo ./install.sh --uninstall  # remove it; add --purge to delete the history too
#
# Settings live in /etc/usage-control.env and the history in
# /var/lib/usage-control; an update keeps both.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
binary=/usr/local/bin/usage-control
unit=/etc/systemd/system/usage-control.service
settings=/etc/usage-control.env

if [[ $EUID -ne 0 ]]; then
  echo "Run this as root, for example: sudo $0 $*" >&2
  exit 1
fi
if ! command -v systemctl > /dev/null || [[ ! -d /run/systemd/system ]]; then
  echo "This machine does not run systemd. Start $here/usage-control by hand, or use Docker (see the README)." >&2
  exit 1
fi

if [[ "${1:-}" == --uninstall ]]; then
  systemctl disable --now usage-control.service 2> /dev/null || true
  rm -f "$binary" "$unit"
  systemctl daemon-reload
  if [[ "${2:-}" == --purge ]]; then
    rm -rf /var/lib/usage-control /var/lib/private/usage-control "$settings"
    echo "usage-control, its settings and its history are removed."
  else
    echo "usage-control is removed. Its settings ($settings) and history (/var/lib/usage-control) are kept; --uninstall --purge deletes them."
  fi
  exit 0
fi

for file in usage-control usage-control.service usage-control.env.example; do
  if [[ ! -f "$here/$file" ]]; then
    echo "$here/$file is missing; run install.sh from the unpacked release archive." >&2
    exit 1
  fi
done

install -m 0755 "$here/usage-control" "$binary"
install -m 0644 "$here/usage-control.service" "$unit"
if [[ ! -f "$settings" ]]; then
  install -m 0644 "$here/usage-control.env.example" "$settings"
fi
systemctl daemon-reload
systemctl enable usage-control.service > /dev/null
systemctl restart usage-control.service

echo "usage-control is running. Open http://<this machine's address>:8080 on the local network (or the port in LISTEN_ADDR)."
echo "Settings: $settings, then: sudo systemctl restart usage-control"
