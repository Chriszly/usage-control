#!/usr/bin/env bash
# Sets up the router the hub in Docker shows, next to compose.yaml:
#
#   sudo ./setup-router.sh
#   docker compose up -d
#
# It asks which router it is, writes HUB_ROUTERS to .env and, for a router
# that needs a login (ASUS, FRITZ!Box, SNMPv3) or an SNMP community other
# than public, the password to router-secrets/router-passwords, which only
# root and the container's user can read. The Linux archive's install.sh
# asks the same and stores the password encrypted; Docker has no key store for that, so here it is protected by
# the file's owner and permissions only.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="$here/.env"
secrets="$here/router-secrets"
# The user the image runs as (distroless nonroot).
container_user=65532

if [[ $EUID -ne 0 ]]; then
  echo "Run this as root, so only the container can read the password: sudo $0" >&2
  exit 1
fi
if [[ ! -t 0 ]]; then
  echo "Run this in a terminal; it asks for the router." >&2
  exit 1
fi

echo "usage-control can show your router as a device: its internet traffic, and depending on the"
echo "router its line, ports, clients, CPU, memory and temperatures."
echo "Privacy: the router is read over the local network only. No data is sent to any server;"
echo "everything stays on this machine."
echo "Which router is it? A login is only asked for where the router needs one."
echo "  1) Any router with UPnP (Technicolor, most internet providers' boxes): internet traffic,"
echo "     no login; UPnP must be switched on in the router's settings"
echo "  2) ASUS, with its login: CPU, memory, ports, Wi-Fi, clients and temperatures"
echo "  3) FRITZ!Box, with a FRITZ!Box user: internet traffic, line, noise margin and clients;"
echo "     allow access for applications in Home Network > Network > Network Settings"
echo "  4) A router with SNMP v2c (MikroTik, OpenWrt, Ubiquiti, business routers): traffic"
echo "     of every port, CPU and memory, with its community"
echo "  5) A router with SNMPv3: the same, with a user and password"
echo "  6) none: stop showing a router"
read -r -p "[1-6, default 1] " kind
kind="${kind:-1}"
if [[ ! "$kind" =~ ^[1-6]$ ]]; then
  echo "Choose one of 1 to 6." >&2
  exit 1
fi

entry=""
password=""
name=""
if [[ "$kind" != 6 ]]; then
  gateway="$(ip -4 route show default 2> /dev/null | awk '{print $3; exit}' || true)"
  read -r -p "The router's address [default ${gateway:-none}] " address
  address="${address:-$gateway}"
  if [[ ! "$address" =~ ^[A-Za-z0-9.-]+$ ]]; then
    echo "The router's address must be a host name or IPv4 address, such as 192.168.1.1." >&2
    exit 1
  fi
  read -r -p "Its name on the page [default Router] " name
  name="${name:-Router}"
  if [[ ! "$name" =~ ^[A-Za-z0-9][A-Za-z0-9\ ._()-]{0,63}$ || "${name,,}" == local ]]; then
    echo "The router's name must start with a letter or digit and hold only letters, digits, spaces and . _ ( ) -, at most 64, and not be Local." >&2
    exit 1
  fi
  case "$kind" in
    1)
      entry="$name=upnp:$address"
      ;;
    2 | 3 | 5)
      case "$kind" in
        2) protocol=asus what="of its web interface" default_user=admin ;;
        3) protocol=fritzbox what="of the FRITZ!Box (System > FRITZ!Box Users)" default_user="" ;;
        5) protocol=snmpv3 what="of SNMPv3" default_user="" ;;
      esac
      read -r -p "The user $what${default_user:+ [default $default_user]} " user
      user="${user:-$default_user}"
      if [[ ! "$user" =~ ^[^[:space:]@=,:]{1,64}$ ]]; then
        echo "The user must be given, without spaces, @, =, , or :." >&2
        exit 1
      fi
      IFS= read -r -s -p "Its password (not shown): " password
      echo
      if [[ -z "$password" ]]; then
        echo "The router needs its password; run setup-router.sh again to enter it." >&2
        exit 1
      fi
      if [[ "$protocol" == snmpv3 && ${#password} -lt 8 ]]; then
        echo "An SNMPv3 password has at least 8 characters." >&2
        exit 1
      fi
      entry="$name=$protocol:$user@$address"
      ;;
    4)
      IFS= read -r -s -p "Its SNMP community (not shown) [default public] " password
      echo
      # The community public needs no secret.
      if [[ "$password" == public ]]; then
        password=""
      fi
      entry="$name=snmp:$address"
      ;;
  esac
fi

if [[ ! -f "$env_file" ]]; then
  cp "$here/.env.example" "$env_file"
  # Owned by whoever ran sudo, so it can be edited without it.
  chown "${SUDO_UID:-0}:${SUDO_GID:-0}" "$env_file"
fi
if grep -q '^HUB_ROUTERS=' "$env_file"; then
  escaped="$(printf '%s' "$entry" | sed 's/[\\&|]/\\&/g')"
  sed -i "s|^HUB_ROUTERS=.*|HUB_ROUTERS=$escaped|" "$env_file"
else
  printf '\nHUB_ROUTERS=%s\n' "$entry" >> "$env_file"
fi

rm -f "$secrets/router-passwords"
if [[ -n "$password" ]]; then
  mkdir -p "$secrets"
  chmod 755 "$secrets"
  (umask 077 && printf '%s=%s\n' "$name" "$password" > "$secrets/router-passwords")
  chown "$container_user:$container_user" "$secrets/router-passwords"
  chmod 400 "$secrets/router-passwords"
fi

if [[ -n "$entry" ]]; then
  echo "Router: $name. Start it with: docker compose up -d"
else
  echo "No router is shown any more. Apply it with: docker compose up -d"
fi
