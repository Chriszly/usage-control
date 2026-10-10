#!/usr/bin/env bash
# Installs or updates usage-control as a systemd service, from the folder of an
# unpacked release archive (usage-control-<version>-linux-<arch>.tar.gz):
#
#   sudo ./install.sh                    # install, or update to this version
#   sudo ./install.sh --addons=cub       # install the Cub edition
#   sudo ./install.sh --addons=cub,power # Grizzly: Cub with the power add-on
#   sudo ./install.sh --addons=          # install without any add-on
#   sudo ./install.sh --uninstall        # remove it; add --purge to delete settings and history too
#
# Add-ons are optional programs that track more than usage-control itself,
# each as a service of its own. Run in a terminal without --addons, the
# install first asks for the edition: Cub is usage-control with the
# lightweight add-ons, Grizzly is Cub with the other add-ons you pick, each
# asked for in turn. Without a terminal, an update keeps the add-ons
# installed before. In --addons, cub stands for the lightweight add-ons.
#
# Settings live in /etc/usage-control.env and the history in
# /var/lib/usage-control; an update keeps both. --uninstall --purge deletes
# them, with any settings made with systemctl edit (usage-control*.service.d).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
binary=/usr/local/bin/usage-control
unit=/etc/systemd/system/usage-control.service
settings=/etc/usage-control.env

# Every add-on there is: those in this archive and those installed before,
# each with what it does for the question at install, from the
# "Description=Usage Control <name> add-on: <what it does>" of its service.
declare -A addon_descriptions=()
for service in "$here"/usage-control-*.service /etc/systemd/system/usage-control-*.service; do
  [[ -f "$service" ]] || continue
  addon="$(basename "$service" .service)"
  addon="${addon#usage-control-}"
  [[ -n "${addon_descriptions[$addon]+set}" ]] && continue
  addon_descriptions[$addon]="$(sed -n 's/^Description=Usage Control [^:]* add-on: //p' "$service")"
done

# The lightweight add-ons, which make up the Cub edition: each reads a few
# small files or counters per poll.
cub_addons=(kernel memory pressure wifi inodes)

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
  # The add-on folder only holds the add-ons' last values; systemd keeps it
  # until the next boot (RuntimeDirectoryPreserve), so it goes now.
  rm -rf /run/usage-control-addons
  systemctl daemon-reload
  if [[ "${2:-}" == --purge ]]; then
    # Settings made with systemctl edit are settings too.
    rm -rf /var/lib/usage-control /var/lib/private/usage-control "$settings" /etc/systemd/system/usage-control*.service.d
    systemctl daemon-reload
    echo "usage-control, its settings and its history are removed."
  else
    echo "usage-control is removed. Its settings ($settings) and history (/var/lib/usage-control) are kept; --uninstall --purge deletes them."
  fi
  exit 0
fi

# The add-ons to install: from --addons, else asked in a terminal, else the
# ones installed before.
declare -A wanted=()
declare -A cub=()
for addon in "${cub_addons[@]}"; do
  cub[$addon]=1
done
if [[ "${1:-}" == --addons=* ]]; then
  IFS=, read -r -a picked <<< "${1#--addons=}"
  for addon in "${picked[@]}"; do
    if [[ $addon == cub ]]; then
      for light in "${cub_addons[@]}"; do
        [[ -f "$here/usage-control-$light.service" ]] && wanted[$light]=1
      done
      continue
    fi
    if [[ -z "${addon_descriptions[$addon]+set}" || ! -f "$here/usage-control-$addon.service" ]]; then
      echo "There is no add-on called '$addon'. Add-ons: cub ${!addon_descriptions[*]}" >&2
      exit 1
    fi
    wanted[$addon]=1
  done
elif [[ -n "${1:-}" ]]; then
  echo "Unknown option $1. Use --addons=<names>, --uninstall or --uninstall --purge." >&2
  exit 1
elif [[ -t 0 ]]; then
  # Grizzly is the default when one of its add-ons is installed already.
  edition=1
  for addon in "${!addon_descriptions[@]}"; do
    if [[ -z "${cub[$addon]+set}" && -f "/etc/systemd/system/usage-control-$addon.service" ]]; then
      edition=2
    fi
  done
  cub_list="${cub_addons[*]}"
  echo "Which edition should this machine run?"
  echo "  1) Cub: usage-control with the lightweight add-ons (${cub_list// /, })"
  echo "  2) Grizzly: Cub, plus the other add-ons you pick next"
  read -r -p "[1/2, default $edition] " answer
  answer="${answer:-$edition}"
  if [[ ! "$answer" =~ ^[12]$ ]]; then
    echo "Choose 1 or 2." >&2
    exit 1
  fi
  for addon in "${cub_addons[@]}"; do
    [[ -f "$here/usage-control-$addon.service" ]] && wanted[$addon]=1
  done
  if [[ $answer == 2 ]]; then
    for addon in "${!addon_descriptions[@]}"; do
      [[ -n "${cub[$addon]+set}" ]] && continue
      # One that is no longer in this archive is removed below.
      [[ -f "$here/usage-control-$addon.service" ]] || continue
      default=n
      [[ -f "/etc/systemd/system/usage-control-$addon.service" ]] && default=y
      read -r -p "Install the $addon add-on? It ${addon_descriptions[$addon]}. [y/n, default $default] " answer
      [[ "${answer:-$default}" == [yY]* ]] && wanted[$addon]=1
    done
  fi
else
  for addon in "${!addon_descriptions[@]}"; do
    [[ -f "$here/usage-control-$addon.service" && -f "/etc/systemd/system/usage-control-$addon.service" ]] && wanted[$addon]=1
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
