#!/usr/bin/env bash
# Installs or updates usage-control as a systemd service, from the folder of an
# unpacked release archive (usage-control-<version>-linux-<arch>.tar.gz):
#
#   sudo ./install.sh                # install, or update to this version
#   sudo ./install.sh --addons=power # install with the power add-on
#   sudo ./install.sh --addons=      # install without any add-on
#   sudo ./install.sh --uninstall    # remove it; add --purge to delete settings and history too
#
# Run in a terminal, it also asks whether to read the router, and for its
# login when the router needs one (ASUS). The login is stored encrypted with
# systemd-creds, and only the service can read it.
#
# Add-ons are optional programs that track more than usage-control itself,
# each as a service of its own. Without --addons, the install asks for each
# one when run in a terminal, and an update keeps the add-ons installed before.
#
# Settings live in /etc/usage-control.env and the history in
# /var/lib/usage-control; an update keeps both. --uninstall --purge deletes
# them, with any settings made with systemctl edit (usage-control*.service.d).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
binary=/usr/local/bin/usage-control
unit=/etc/systemd/system/usage-control.service
settings=/etc/usage-control.env
# The router passwords, encrypted with systemd-creds, and the setting that
# hands them to the service.
router_passwords=/etc/usage-control-router-passwords.cred
router_dropin=/etc/systemd/system/usage-control.service.d/router-passwords.conf

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
    rm -rf /var/lib/usage-control /var/lib/private/usage-control "$settings" "$router_passwords" /etc/systemd/system/usage-control*.service.d
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
if [[ "${1:-}" == --addons=* ]]; then
  IFS=, read -r -a picked <<< "${1#--addons=}"
  for addon in "${picked[@]}"; do
    if [[ -z "${addon_descriptions[$addon]+set}" || ! -f "$here/usage-control-$addon.service" ]]; then
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
    # One that is no longer in this archive is removed below.
    [[ -f "$here/usage-control-$addon.service" ]] || continue
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

# The router to read: asked in a terminal only, and on an update only when
# asked to set it up again.
router_entry=""
router_password=""
router_name=""
if [[ -t 0 && "${1:-}" != --addons=* ]]; then
  current="$(sed -n 's/^HUB_ROUTERS=//p' "$settings" 2> /dev/null | tail -n 1)"
  echo
  echo "usage-control can also show your router: its internet traffic, and with an ASUS router's"
  echo "login its CPU, memory, ports, Wi-Fi, clients and temperatures."
  echo "Privacy: the router is read over the local network only. No data is sent to any server;"
  echo "everything stays on this machine, and the router's password is stored encrypted."
  if [[ -n "$current" ]]; then
    question="Set up the router again? It is now $current. [y/n, default n] "
  else
    question="Show your router? [y/n, default n] "
  fi
  read -r -p "$question" answer
  if [[ "${answer:-n}" == [yY]* ]]; then
    echo "Which router is it?"
    echo "  1) ASUS, with its login: CPU, memory, ports, Wi-Fi, clients and temperatures"
    echo "  2) ASUS without a login, Technicolor, FRITZ!Box or another: internet traffic over UPnP,"
    echo "     which must be switched on in the router's settings"
    read -r -p "[1/2, default 2] " kind
    gateway="$(ip -4 route show default 2> /dev/null | awk '{print $3; exit}' || true)"
    read -r -p "The router's address [default ${gateway:-none}] " address
    address="${address:-$gateway}"
    if [[ ! "$address" =~ ^[A-Za-z0-9.-]+$ ]]; then
      echo "The router's address must be a host name or IPv4 address, such as 192.168.1.1." >&2
      exit 1
    fi
    read -r -p "Its name on the page [default Router] " router_name
    router_name="${router_name:-Router}"
    if [[ "$router_name" == *[=,]* ]]; then
      echo "The router's name cannot hold = or ,." >&2
      exit 1
    fi
    if [[ "${kind:-2}" == 1 ]]; then
      read -r -p "The user of its web interface [default admin] " user
      user="${user:-admin}"
      if [[ ! "$user" =~ ^[^[:space:]@=,:]{1,64}$ ]]; then
        echo "The user cannot hold spaces, @, =, , or :." >&2
        exit 1
      fi
      read -r -s -p "Its password (not shown): " router_password
      echo
      if [[ -z "$router_password" ]]; then
        echo "The ASUS router needs its password; run install.sh again to enter it." >&2
        exit 1
      fi
      router_entry="$router_name=asus:$user@$address"
    else
      router_entry="$router_name=upnp:$address"
    fi
  fi
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
if [[ -n "$router_entry" ]]; then
  # Settings files from before routers have no HUB_ROUTERS line yet.
  if grep -q '^HUB_ROUTERS=' "$settings"; then
    escaped="$(printf '%s' "$router_entry" | sed 's/[\\&|]/\\&/g')"
    sed -i "s|^HUB_ROUTERS=.*|HUB_ROUTERS=$escaped|" "$settings"
  else
    printf '\nHUB_ROUTERS=%s\n' "$router_entry" >> "$settings"
  fi
  rm -f "$router_passwords" "$router_dropin"
  if [[ -n "$router_password" ]]; then
    mkdir -p "$(dirname "$router_dropin")"
    # Encrypted with the machine's own key (and its TPM, where there is
    # one), so the file is of no use elsewhere; systemd decrypts it only for
    # the service, into a folder only the service can read.
    if command -v systemd-creds > /dev/null && printf '%s=%s\n' "$router_name" "$router_password" |
      systemd-creds encrypt --name=router-passwords - "$router_passwords" 2> /dev/null; then
      chmod 600 "$router_passwords"
      printf '[Service]\nLoadCredentialEncrypted=router-passwords:%s\n' "$router_passwords" > "$router_dropin"
    else
      # systemd before 250 cannot encrypt it: kept readable by root only.
      (umask 077 && printf '%s=%s\n' "$router_name" "$router_password" > "$router_passwords")
      printf '[Service]\nLoadCredential=router-passwords:%s\n' "$router_passwords" > "$router_dropin"
      echo "This systemd cannot encrypt the router's password (systemd-creds needs systemd 250 or newer); it is stored readable by root only in $router_passwords." >&2
    fi
  fi
fi
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
if [[ -n "$router_entry" ]]; then
  echo "Router: ${router_entry%%=*}, shown as a device on the page"
fi
echo "Settings: $settings, then: sudo systemctl restart usage-control"
