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
# A failing step shows what the services logged.
trap 'journalctl -u usage-control -u usage-control-power -u usage-control-wifi --no-pager | tail -40 >&2' ERR
tar -xzf "$archive" -C "$work"
folder="$(find "$work" -mindepth 1 -maxdepth 1 -type d)"

# Waits until the service answers at path and prints the answer.
answer() {
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:9393$1" 2> /dev/null; then
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

# The power add-on runs as a service of its own and writes to the add-on
# folder; an update without --addons keeps it.
"$folder/install.sh" --addons=power
systemctl is-active --quiet usage-control-power || { journalctl -u usage-control-power --no-pager | tail -20 >&2; echo "the power add-on is not running" >&2; exit 1; }
for _ in $(seq 1 10); do
  [[ -f /run/usage-control-addons/power.json ]] && break
  sleep 1
done
test -f /run/usage-control-addons/power.json || { echo "the power add-on wrote no report" >&2; exit 1; }
"$folder/install.sh" < /dev/null
systemctl is-active --quiet usage-control-power || { echo "the update removed the power add-on" >&2; exit 1; }
answer /api/metrics > /dev/null
# The Wi-Fi add-on runs too; a runner without Wi-Fi gets a report without
# values.
"$folder/install.sh" --addons=wifi
systemctl is-active --quiet usage-control-wifi || { journalctl -u usage-control-wifi --no-pager | tail -20 >&2; echo "the Wi-Fi add-on is not running" >&2; exit 1; }
for _ in $(seq 1 10); do
  [[ -f /run/usage-control-addons/wifi.json ]] && break
  sleep 1
done
test -f /run/usage-control-addons/wifi.json || { echo "the Wi-Fi add-on wrote no report" >&2; exit 1; }
"$folder/install.sh" --addons=
if systemctl cat usage-control-power > /dev/null 2>&1 || [[ -e /usr/local/bin/usage-control-power ]]; then
  echo "--addons= left the power add-on behind" >&2
  exit 1
fi
"$folder/install.sh" --addons=power

"$folder/install.sh" --uninstall --purge
if systemctl cat usage-control > /dev/null 2>&1 || systemctl cat usage-control-power > /dev/null 2>&1 ||
  [[ -e /usr/local/bin/usage-control || -e /usr/local/bin/usage-control-power || -e /etc/usage-control.env ]]; then
  echo "the uninstall left usage-control behind" >&2
  exit 1
fi
echo "Install, update, the power add-on and uninstall work."
