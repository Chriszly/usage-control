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
trap 'journalctl -u usage-control -u usage-control-power -u usage-control-pressure -u usage-control-kernel -u usage-control-gpu -u usage-control-inodes --no-pager | tail -40 >&2' ERR
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
# The pressure add-on reads /proc/pressure, which a kernel without pressure
# stall information lacks; it then runs but reports nothing. The kernel add-on
# reads /proc, which every runner has. All three add-ons are installed, so
# --addons= below has to remove them all.
"$folder/install.sh" --addons=power,pressure,kernel
systemctl is-active --quiet usage-control-pressure || { journalctl -u usage-control-pressure --no-pager | tail -20 >&2; echo "the pressure add-on is not running" >&2; exit 1; }
if [[ -f /proc/pressure/cpu ]]; then
  for _ in $(seq 1 10); do
    grep -qs '"cpu-some"' /run/usage-control-addons/pressure.json && break
    sleep 1
  done
  grep -qs '"cpu-some"' /run/usage-control-addons/pressure.json || { echo "the pressure add-on reported no CPU pressure" >&2; exit 1; }
fi
systemctl is-active --quiet usage-control-kernel || { journalctl -u usage-control-kernel --no-pager | tail -20 >&2; echo "the kernel add-on is not running" >&2; exit 1; }
for _ in $(seq 1 10); do
  grep -qs '"tcp-established"' /run/usage-control-addons/kernel.json && break
  sleep 1
done
grep -qs '"tcp-established"' /run/usage-control-addons/kernel.json || { echo "the kernel add-on wrote no report" >&2; exit 1; }
"$folder/install.sh" --addons=
if systemctl cat usage-control-power > /dev/null 2>&1 || [[ -e /usr/local/bin/usage-control-power ]]; then
  echo "--addons= left the power add-on behind" >&2
  exit 1
fi
if systemctl cat usage-control-pressure > /dev/null 2>&1 || [[ -e /usr/local/bin/usage-control-pressure ]]; then
  echo "--addons= left the pressure add-on behind" >&2
  exit 1
fi
if systemctl cat usage-control-kernel > /dev/null 2>&1 || [[ -e /usr/local/bin/usage-control-kernel ]]; then
  echo "--addons= left the kernel add-on behind" >&2
  exit 1
fi

# The gpu add-on runs without an NVIDIA GPU too, and then reports nothing.
"$folder/install.sh" --addons=gpu
systemctl is-active --quiet usage-control-gpu || { journalctl -u usage-control-gpu --no-pager | tail -20 >&2; echo "the gpu add-on is not running" >&2; exit 1; }
for _ in $(seq 1 10); do
  [[ -f /run/usage-control-addons/gpu.json ]] && break
  sleep 1
done
test -f /run/usage-control-addons/gpu.json || { echo "the gpu add-on wrote no report" >&2; exit 1; }
"$folder/install.sh" --addons=
if systemctl cat usage-control-gpu > /dev/null 2>&1 || [[ -e /usr/local/bin/usage-control-gpu ]]; then
  echo "--addons= left the gpu add-on behind" >&2
  exit 1
fi
"$folder/install.sh" --addons=power

# The inodes add-on runs next to them and reports at least the root
# filesystem; the power add-on stays installed. The kernel add-on stays for
# the uninstall to remove.
"$folder/install.sh" --addons=power,inodes,kernel
systemctl is-active --quiet usage-control-inodes || { journalctl -u usage-control-inodes --no-pager | tail -20 >&2; echo "the inodes add-on is not running" >&2; exit 1; }
for _ in $(seq 1 10); do
  grep -qs '"label":"/"' /run/usage-control-addons/inodes.json && break
  sleep 1
done
grep -qs '"label":"/"' /run/usage-control-addons/inodes.json || { echo "the inodes add-on reported no root filesystem" >&2; exit 1; }
systemctl is-active --quiet usage-control-power || { echo "adding the inodes add-on stopped the power add-on" >&2; exit 1; }

"$folder/install.sh" --uninstall --purge
if systemctl cat usage-control > /dev/null 2>&1 || systemctl cat usage-control-power > /dev/null 2>&1 ||
  systemctl cat usage-control-inodes > /dev/null 2>&1 || systemctl cat usage-control-kernel > /dev/null 2>&1 ||
  [[ -e /usr/local/bin/usage-control || -e /usr/local/bin/usage-control-power || -e /usr/local/bin/usage-control-inodes ||
  -e /usr/local/bin/usage-control-kernel || -e /etc/usage-control.env ]]; then
  echo "the uninstall left usage-control behind" >&2
  exit 1
fi
echo "Install, update, the power, pressure, kernel, gpu and inodes add-ons and uninstall work."
