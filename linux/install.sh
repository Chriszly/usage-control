#!/usr/bin/env bash
# Installs or updates usage-control as a systemd service, from the folder of an
# unpacked release archive (usage-control-<version>-linux-<arch>.tar.gz):
#
#   sudo ./install.sh                # install, or update to this version
#   sudo ./install.sh --addons=power # install with the power add-on
#   sudo ./install.sh --addons=      # install without any add-on
#   sudo ./install.sh --uninstall    # remove it; add --purge to delete the history too
#
# Add-ons are optional programs that track more than usage-control itself,
# each as a service of its own. Without --addons, the install asks for each
# one when run in a terminal, and an update keeps the add-ons installed before.
#
# Settings live in /etc/usage-control.env and the history in
# /var/lib/usage-control; an update keeps both.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
binary=/usr/local/bin/usage-control
unit=/etc/systemd/system/usage-control.service
settings=/etc/usage-control.env

# Every add-on there is, with what it does for the question at install.
declare -A addon_descriptions=(
  [power]="reads the power the machine draws (Raspberry Pi 5, Intel and AMD CPUs, NVIDIA GPUs); runs as root without capabilities"
)

if [[ $EUID -ne 0 ]]; then
  echo "Run this as root, for example: sudo $0 $*" >&2
  exit 1
fi
if ! command -v systemctl > /dev/null || [[ ! -d /run/systemd/system ]]; then
  echo "This machine does not run systemd. Start $here/usage-control by hand, or use Docker (see the README)." >&2
  exit 1
fi

# Removes one add-on's service and program.
remove_addon() {
  systemctl disable --now "usage-control-$1.service" 2> /dev/null || true
  rm -f "/usr/local/bin/usage-control-$1" "/etc/systemd/system/usage-control-$1.service"
}

if [[ "${1:-}" == --uninstall ]]; then
  for addon in "${!addon_descriptions[@]}"; do
    remove_addon "$addon"
  done
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

# The add-ons to install: from --addons, else asked in a terminal, else the
# ones installed before.
declare -A wanted=()
if [[ "${1:-}" == --addons=* ]]; then
  IFS=, read -r -a picked <<< "${1#--addons=}"
  for addon in "${picked[@]}"; do
    if [[ -z "${addon_descriptions[$addon]+set}" ]]; then
      echo "There is no add-on called '$addon'. Add-ons: ${!addon_descriptions[*]}" >&2
      exit 1
    fi
    wanted[$addon]=1
  done
elif [[ -n "${1:-}" ]]; then
  echo "Unknown option $1. Use --addons=<names>, --uninstall or --uninstall --purge." >&2
  exit 1
else
  for addon in "${!addon_descriptions[@]}"; do
    installed=no
    [[ -f "/etc/systemd/system/usage-control-$addon.service" ]] && installed=yes
    if [[ -t 0 ]]; then
      default=n
      [[ $installed == yes ]] && default=y
      read -r -p "Install the $addon add-on? It ${addon_descriptions[$addon]}. [y/n, default $default] " answer
      [[ "${answer:-$default}" == [yY]* ]] && wanted[$addon]=1
    elif [[ $installed == yes ]]; then
      wanted[$addon]=1
    fi
  done
fi

for file in usage-control usage-control.service usage-control.env.example; do
  if [[ ! -f "$here/$file" ]]; then
    echo "$here/$file is missing; run install.sh from the unpacked release archive." >&2
    exit 1
  fi
done
for addon in "${!wanted[@]}"; do
  for file in "usage-control-$addon" "usage-control-$addon.service"; do
    if [[ ! -f "$here/$file" ]]; then
      echo "$here/$file is missing; run install.sh from the unpacked release archive." >&2
      exit 1
    fi
  done
done

install -m 0755 "$here/usage-control" "$binary"
install -m 0644 "$here/usage-control.service" "$unit"
if [[ ! -f "$settings" ]]; then
  install -m 0644 "$here/usage-control.env.example" "$settings"
fi
for addon in "${!addon_descriptions[@]}"; do
  if [[ -n "${wanted[$addon]+set}" ]]; then
    install -m 0755 "$here/usage-control-$addon" "/usr/local/bin/usage-control-$addon"
    install -m 0644 "$here/usage-control-$addon.service" "/etc/systemd/system/usage-control-$addon.service"
  else
    remove_addon "$addon"
  fi
done
systemctl daemon-reload
# Several installs in a row would hit systemd's limit of starts in a short
# time, which reset-failed clears.
systemctl reset-failed usage-control.service 2> /dev/null || true
systemctl enable usage-control.service > /dev/null
systemctl restart usage-control.service
for addon in "${!wanted[@]}"; do
  systemctl reset-failed "usage-control-$addon.service" 2> /dev/null || true
  systemctl enable "usage-control-$addon.service" > /dev/null
  systemctl restart "usage-control-$addon.service"
done

echo "usage-control is running. Open http://<this machine's address>:9393 on the local network (or the port in LISTEN_ADDR)."
if [[ ${#wanted[@]} -gt 0 ]]; then
  echo "Add-ons: ${!wanted[*]}"
fi
echo "Settings: $settings, then: sudo systemctl restart usage-control"
