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
# Run in a terminal, it also asks whether to read the router, and for its
# login when the router needs one (ASUS, FRITZ!Box, SNMPv3) or its SNMP
# community. The login is stored encrypted with
# systemd-creds, and only the service can read it.
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

# The router to read: asked in a terminal only, and on an update only when
# asked to set it up again. Not on a device that only serves its usage to a
# hub (DATA_ONLY), which shows no routers and would not start with one.
router_entry=""
router_password=""
router_name=""
router_remove=""
kind=""
data_only="$(sed -n 's/^DATA_ONLY=//p' "$settings" 2> /dev/null | tail -n 1 | tr -d '[:space:]' || true)"
if [[ -t 0 && "${1:-}" != --addons=* && ! "$data_only" =~ ^(1|t|T|true|TRUE|True)$ ]]; then
  # No settings file yet on a first install.
  current="$(sed -n 's/^HUB_ROUTERS=//p' "$settings" 2> /dev/null | tail -n 1 || true)"
  echo
  echo "usage-control can also show your router: its internet traffic, and depending on the router"
  echo "its line, ports, clients, CPU, memory and temperatures."
  echo "Read from the router over the local network only. No data is sent to any server;"
  echo "everything stays on your own devices, and a router password is stored encrypted where"
  echo "the system allows it."
  if [[ -n "$current" ]]; then
    question="Set up the router again? It is now $current. [y/n, default n] "
  else
    question="Show your router? [y/n, default n] "
  fi
  read -r -p "$question" answer
  if [[ "${answer:-n}" == [yY]* ]]; then
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
  fi
  if [[ "$kind" == 6 ]]; then
    router_remove=1
  elif [[ -n "$kind" ]]; then
    gateway="$(ip -4 route show default 2> /dev/null | awk '{print $3; exit}' || true)"
    read -r -p "The router's address [default ${gateway:-none}] " address
    address="${address:-$gateway}"
    if [[ ! "$address" =~ ^[A-Za-z0-9.-]+$ ]]; then
      echo "The router's address must be a host name or IPv4 address, such as 192.168.1.1." >&2
      exit 1
    fi
    read -r -p "Its name on the page [default Router] " router_name
    router_name="${router_name:-Router}"
    if [[ ! "$router_name" =~ ^[A-Za-z0-9][A-Za-z0-9\ ._()-]{0,63}$ || "${router_name,,}" == local ]]; then
      echo "The router's name must start with a letter or digit and hold only letters, digits, spaces and . _ ( ) -, at most 64, and not be Local." >&2
      exit 1
    fi
    case "$kind" in
      1)
        router_entry="$router_name=upnp:$address"
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
        IFS= read -r -s -p "Its password (not shown): " router_password
        echo
        if [[ -z "$router_password" ]]; then
          echo "The router needs its password; run install.sh again to enter it." >&2
          exit 1
        fi
        if [[ "$protocol" == snmpv3 && ${#router_password} -lt 8 ]]; then
          echo "An SNMPv3 password has at least 8 characters." >&2
          exit 1
        fi
        router_entry="$router_name=$protocol:$user@$address"
        ;;
      4)
        IFS= read -r -s -p "Its SNMP community (not shown) [default public] " router_password
        echo
        # The community public needs no secret.
        if [[ "$router_password" == public ]]; then
          router_password=""
        fi
        router_entry="$router_name=snmp:$address"
        ;;
    esac
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
if [[ -n "$router_entry" || -n "$router_remove" ]]; then
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
      systemd-creds encrypt --name=router-passwords - "$router_passwords"; then
      chmod 600 "$router_passwords"
      printf '[Service]\nLoadCredentialEncrypted=router-passwords:%s\n' "$router_passwords" > "$router_dropin"
    else
      # systemd before 250 cannot encrypt it: kept readable by root only
      # (LoadCredential needs systemd 247 or newer).
      (umask 077 && printf '%s=%s\n' "$router_name" "$router_password" > "$router_passwords")
      printf '[Service]\nLoadCredential=router-passwords:%s\n' "$router_passwords" > "$router_dropin"
      echo "The router's password could not be encrypted with systemd-creds (systemd 250 or newer); it is stored readable by root only in $router_passwords." >&2
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
elif [[ -n "$router_remove" ]]; then
  echo "No router is shown any more."
fi
echo "Settings: $settings, then: sudo systemctl restart usage-control"
