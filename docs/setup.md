# Setup

This page walks through a typical setup: a Raspberry Pi as the hub that shows everything, Windows PCs that only report to it, and other Linux machines either way. Every step also works on its own: a single Pi with nothing else is a complete install.

- [Plan the setup](#plan-the-setup)
- [1. Set up the hub with Docker](#1-set-up-the-hub-with-docker)
- [Or: set up a Linux machine without Docker](#or-set-up-a-linux-machine-without-docker)
- [2. Add a Windows PC](#2-add-a-windows-pc)
- [3. Add another Linux machine](#3-add-another-linux-machine)
- [4. Add the devices on the hub](#4-add-the-devices-on-the-hub)
- [Run from source](#run-from-source)
- [Settings](#settings)
- [Updating](#updating)
- [Troubleshooting](#troubleshooting)

## Plan the setup

Only add devices you own, or devices whose owner and users have agreed to it. The page shows when each device was on and how busy it was, so monitoring a device someone else uses needs their knowledge and, where the law requires it, their consent; see [Intended use](../README.md#intended-use).

| Device | Install | Role |
| --- | --- | --- |
| Raspberry Pi or other always-on Linux machine | Docker image, or the Linux archive with a systemd service | **hub**: shows every device and keeps all history |
| Windows PC (x64 or ARM) | MSI installer | data only: reports to the hub |
| Other Linux machine | Docker with `DATA_ONLY=true`, or the Linux archive | data only, or with its own page |
| macOS | [from source](#run-from-source) | not packaged yet |

All devices must be on the same local network as the hub. They need no internet access, except the hub for its once-a-day version check, which can be turned off. How they talk to each other is in [How it works](architecture.md).

usage-control listens on port **9393** by default.

## 1. Set up the hub with Docker

On the Pi, with Docker and the Compose plugin installed, get `compose.yaml` and `.env.example` from this repository (a `git clone` is the easiest), then:

```bash
cp .env.example .env   # optional, to change settings
docker compose pull
docker compose up -d
```

This runs the published image `ghcr.io/chriszly/usage-control` for arm64 and amd64; the Pi builds nothing. Open `http://<the Pi's address>:9393` from a phone or PC on the same network.

What `compose.yaml` sets up:

- the host's `/proc` and `/sys` mounted read-only, so the container measures the Pi, not itself
- `/etc/localtime` mounted read-only, so the page shows the Pi's time zone
- a `data` volume for the [database](database.md), kept across updates
- a read-only container running as a non-root user, with every capability dropped and `no-new-privileges`
- the port published over IPv4 only, so the program sees each client's real address. A browser that tries IPv6 first falls back to IPv4 by itself

`IMAGE_TAG` in `.env` picks the version: `latest` (default) is the newest release, a release such as `0.1.0` stays on it, and `main` follows every change on the main branch, for testing. `docker compose up -d --build` builds the image from the checkout instead.

### Show more disks

By default the page shows the disk Docker keeps its data on, usually the system disk. The container sees no other host folder unless it is mounted. To show a USB disk mounted at `/mnt/usb`, mount it read-only at the same path in `compose.yaml` and list it in `.env`:

```yaml
    volumes:
      - /mnt/usb:/mnt/usb:ro
```

```bash
DISK_PATHS=/,/mnt/usb
```

usage-control refuses to start when a path in `DISK_PATHS` cannot be read, and says which one.

### Open the page by name

The page answers when opened by an IP address, `localhost`, the machine's hostname or any `.local` name, such as `http://raspberrypi.local:9393`. Inside Docker the hostname is the container's, so a bare `http://raspberrypi:9393` or a name from the router's DNS has to be listed:

```bash
ALLOWED_HOSTS=raspberrypi,pi.fritz.box
```

Other names are refused with `421 Misdirected Request` ([why](architecture.md#local-network-only)).

## Or: set up a Linux machine without Docker

Every [release](https://github.com/Chriszly/usage-control/releases) after 0.1.0 has an archive for Linux: `usage-control-<version>-linux-arm64.tar.gz` for a Raspberry Pi with a 64-bit system, `-linux-amd64.tar.gz` for a PC. It holds the program, a systemd service and an install script:

```bash
tar -xzf usage-control-<version>-linux-arm64.tar.gz
cd usage-control-<version>-linux-arm64
sudo ./install.sh
```

The service starts right away and at every boot, on port 9393. Settings go in `/etc/usage-control.env`, followed by `sudo systemctl restart usage-control`. The history is kept in `/var/lib/usage-control`. Running natively, it sees every disk and needs no mounts. Like the container, the service runs as an unprivileged throwaway user that can write only its own folder.

To update, unpack the newer archive and run its `install.sh` again; settings and history are kept. `sudo ./install.sh --uninstall` removes it, and `--uninstall --purge` also deletes its settings and history.

### Add-ons

Add-ons are optional programs that track more than usage-control itself does. Each one runs as a service of its own, so a device only runs the ones picked for it. In a terminal, `install.sh` asks for each add-on; `--addons=power` picks them without asking, `--addons=` installs none, and an update without the option keeps the add-ons installed before.

| Add-on | What it adds |
| --- | --- |
| `power` | the power the machine draws, with its history: a Raspberry Pi 5 in total (through `vcgencmd`), Intel and AMD CPUs per package and memory (RAPL), sensors the kernel lists under hwmon (such as AMD GPUs) and NVIDIA GPUs (through `nvidia-smi`). On Windows: the energy meters Windows offers (RAPL and laptop meters), the battery while the PC runs on it, and NVIDIA GPUs. On Linux it runs as root, since newer kernels let only root read the CPU's energy counters, but without any capability or network access |
| `gpu` | more about NVIDIA graphics cards than the page shows, from one `nvidia-smi` call every 5 seconds: the fan, the graphics and memory clocks and the use of the video encoder and decoder, with their history, and the performance state and power limit, without. Without `nvidia-smi` it reports nothing. It runs as root, like every add-on, since they share a folder only root writes, but without any capability or network access |
| `inodes` | Linux only: how many inodes (files and folders) each real mounted filesystem has in use, in percent with its history, by mount point. A disk full of small files can run out of inodes while it still has free space, and then no new file fits. Memory and kernel filesystems, network shares (whose server may not answer), read-only images and filesystems without a fixed number of inodes, such as btrfs and vfat, are left out. It runs as root like the other add-ons, without any capability or network access |
| `pressure` | how much of the time tasks had to wait for the CPU, for memory and for disks and other I/O, with its history (Linux pressure stall information from `/proc/pressure`; *tasks waiting* is the share of the last ten seconds in which at least one task waited, *all tasks stalled* the share in which no task could run). Kernels without PSI, or with it switched off, report nothing: Raspberry Pi OS switches it off, so there add `psi=1`, after a space, to the end of the one line in `/boot/firmware/cmdline.txt` and restart the Pi. Reading needs no privileges: with the Linux archive it runs as root, without any capability or network access, only to write to the shared add-on folder, and its container runs as the image's user. On Windows, which measures no such waiting, it reports the closest signals its performance counters offer instead, under names of their own: *CPU: threads waiting* (threads ready to run that wait for a processor, at the moment of the reading), *Memory: pages read from disk* (per second, for hard page faults, each of which makes a program wait for memory that was paged out or for a mapped file) and *Disks: busy* (the share of time the disks had work, averaged over all disks). These are approximations: they show that something queues, not for how long anything stalled, so they cannot be compared with the Linux percentages |
| `kernel` | what the kernel is busy with, with its history: context switches, interrupts and new processes and threads per second (`/proc/stat`, which counts each new thread as a process too), open files (`/proc/sys/fs/file-nr`), sockets in use (`/proc/net/sockstat`), established TCP connections and TCP retransmissions per second (`/proc/net/snmp`). It reads the host's network values through `/proc/1/net`, since `/proc/net` shows only the network of the program that reads it. With the Linux archive it runs as root only to write to the add-on folder, without any capability or network access; its container runs as the image's user. On Windows it reads the closest values Windows offers: context switches and interrupts per second (performance counters), open handles instead of open files (`GetPerformanceInfo`; handles are files and every other kernel object), established TCP connections and TCP retransmissions per second, both over IPv4 and IPv6 as on Linux (`GetTcpStatisticsEx`). Windows counts no new processes and no sockets in use, so those are missing there |
| `wifi` | the link quality (in percent, with its history) and signal (in dBm, only its current value, as it is below the 0 the charts start at) of each connected Wi-Fi interface; interfaces that are not connected report nothing. On Linux it reads `/proc/net/wireless`, where the quality is the driver's link quality; Wi-Fi 7 adapters (such as the Intel BE200) report nothing there, as the kernel keeps that file only for older Wi-Fi, so they show nothing. From Linux 6.4 on, reading it makes the kernel log once per boot that the add-on uses "wireless extensions", whatever the adapter; that is expected. The file it reads is readable by anyone: with the Linux archive it runs as root only to write to the shared add-on folder, without any capability or network access, and its container runs as the image's user. On Windows it asks Windows' own WLAN API (`wlanapi.dll`) and names each interface by its adapter's description, cut short where it is too long for a label; it keeps the values by the adapter's interface GUID, so two adapters of the same model stay apart. Its quality is Windows' signal quality (0 % at -100 dBm, 100 % at -50 dBm), so the two systems rate the same signal a little differently. If Windows withholds the current connection (Windows 11 24H2 and later do so from programs without location access), the quality is worked out from the signal by that same rule. On a Windows without the WLAN AutoConfig service or a Wi-Fi adapter, such as most servers, it reports nothing. The link's bitrate is left out, as Linux reports it only through netlink |
| `memory` | memory details beyond used, cached and swap, with their history. On Linux, from `/proc/meminfo` and `/proc/vmstat`: dirty memory and memory being written back, the kernel's slab caches, shared memory, page tables and committed memory in bytes, page faults and major page faults (read from disk) per second, and swapping in and out in bytes per second. Reading needs no privilege: with the Linux archive it runs as root only because the add-on folder is root's, without any capability or network access, and its container runs as the image's user. On Windows, from the Memory performance counters, the nearest Windows has: modified memory (waiting to be written), the kernel's paged and nonpaged pools in place of the slab caches, committed memory, page faults per second, disk reads for page faults per second in place of major page faults (one read can bring in several pages), and paging in and out in bytes per second, which counts the page file and memory-mapped files, not only swapping. Windows has no counter for memory being written back, shared memory or page tables, so those are left out |
| `ports` | the TCP and UDP ports the machine listens on, each with the addresses it listens on (`0.0.0.0` and `::` for all), live only, and how many there are, with its history. UDP ports in the range the system hands out to programs that only send (32768 to 60999 on Linux, the kernel's default `ip_local_port_range`; the add-on has no network of its own and so does not see a range changed on the host, and 49152 to 65535 on Windows) are left out, as browsers and DNS lookups open them for a moment; a service that listens on UDP in that range, such as WireGuard on 51820, is left out with them. It reads the host's socket tables in `/proc`, which any user may read, and shows no process names: finding them would need rights over every process. With the Linux archive it runs as root only to write the add-on folder, without any capability or network access; its container runs as the image's user |
| `smart` | the health of the disks, read straight from them with no program to install: for each SATA and NVMe disk whether its own SMART check passes, and with their history its temperature, power-on hours, reallocated sectors (SATA), media errors and wear (NVMe). It reads each disk every 30 minutes, as smartd does: a read counts as use of the disk, so reading more often would keep the system from switching an unused disk off. Before each read it asks a SATA disk whether it sleeps, and leaves it asleep if it does. SAS disks and disks behind RAID controllers are not read, and USB disks are left out on purpose, as some USB adapters reset the disk when they get the commands. Each disk's history is kept under its serial number, so it stays with the disk when names such as `sda` change at a restart. On Linux it runs as root with only the capabilities the disk commands need (`CAP_SYS_RAWIO` for SATA disks, `CAP_SYS_ADMIN` for NVMe disks), read-only access to just those disks, a system call filter, and no network access. `CAP_SYS_ADMIN` allows much more than NVMe commands and systemd cannot grant it only where there are NVMe disks, so it is granted everywhere, with the system call filter keeping it from mounting, loading kernel modules and the like; without NVMe disks, `systemctl edit usage-control-smart` with `CapabilityBoundingSet=~CAP_SYS_ADMIN` takes it away. On Windows it runs as LocalSystem, since Windows lets only administrators send SMART commands |
| `containers` | the CPU (in percent of the whole machine) and memory of each running Docker or Podman container, with their history, up to 64 containers. Linux with cgroup v2 only (current Raspberry Pi OS, Debian, Ubuntu and Fedora); it reads the kernel's cgroup files, never the Docker socket, which would give it control of the machine. Docker's containers are shown by name, read from `/var/lib/docker/containers`, others by their short id; the values of a container stay together under its name when it is made anew, as by an update. Where Docker keeps its data elsewhere, `DOCKER_DIR` names that folder, as `docker info` shows it under *Docker Root Dir* (for the archive, add `Environment=DOCKER_DIR=<folder>` under `[Service]` with `sudo systemctl edit usage-control-containers`; the archive's service cannot read folders below `/home`, so rootless Docker's names show only with the container). Where the kernel counts no memory per container, as Raspberry Pi OS's does unless `cgroup_enable=memory` is added to `/boot/firmware/cmdline.txt`, it shows only the CPU. It runs as root for those names, but without any capability or network access, and only reads. Not on Windows |
| `processes` | the ten processes using the most CPU (percent of the whole machine since the previous read) and the ten holding the most memory (resident, in bytes), live only without history since processes come and go. It reads `/proc/<pid>/stat`, one small file per process (about 1 ms for 100 processes per read). With the Linux archive it runs as root only to write the add-on folder, without any capability or network access, and its container as the image's user; the files it reads every user may read |

Add-ons write what they read to `/run/usage-control-addons`, which usage-control shows as [extras](data.md#extras). A hub shows and keeps them like any other extras; a hub from before extras ignores them. On Windows, the installer offers them as boxes ([step 2](#2-add-a-windows-pc)) and they write to `C:\ProgramData\Usage Control\addons`. In Docker, the image carries them too, and `COMPOSE_PROFILES=power` in `.env` starts the power add-on as a container of its own next to usage-control. It runs as root without capabilities or network and writes to an in-memory volume. In a container it reads only RAPL and hwmon: a Raspberry Pi 5's total and NVIDIA GPUs need `vcgencmd` and `nvidia-smi` from the host, so for those, use the Linux archive. `COMPOSE_PROFILES=pressure` (or `power,pressure` for both) starts the pressure add-on as a container too, reading the host's `/proc`, mounted read-only at `/host/proc`; it needs no root and runs as the image's user, like usage-control. It works in a container as it does outside one. `COMPOSE_PROFILES=kernel` (or `power,kernel`) starts the kernel add-on as a container too; it reads the host's `/proc`, mounted read-only, and its network values from `/host/proc/1/net`, the host's own network rather than the container's. It needs no root and runs as the image's user, like usage-control. `COMPOSE_PROFILES=wifi` starts the Wi-Fi add-on as a container too; it reads the host's `/proc/1/net/wireless` from the mounted `/proc`, so it sees the host's Wi-Fi without network of its own, and as that file is readable by anyone, it runs as the image's user, like usage-control. `COMPOSE_PROFILES=memory` (or `power,memory` for both) starts the memory add-on as a container too, reading the host's `/proc` read-only; it needs no root and runs as the image's user, like usage-control. `COMPOSE_PROFILES=ports` starts the ports add-on as a container too; it reads the host's ports from the host's `/proc`, mounted read-only, and needs no root, so it runs as the image's user, like usage-control. Both: `COMPOSE_PROFILES=power,ports`. `COMPOSE_PROFILES=containers` starts the containers add-on, which mounts the host's `/sys/fs/cgroup` and the `containers` folder in Docker's data folder read-only: `/var/lib/docker/containers`, or below `DOCKER_DIR` in `.env`. Compose does not create that folder where it is missing, so where there is none, as with Podman, the add-on does not start; use the Linux archive there. It runs as root inside its container, so it may read the containers' names, but without capabilities or network, never with the Docker socket. Several add-ons go comma-separated, such as `COMPOSE_PROFILES=power,containers`. `COMPOSE_PROFILES=processes` starts the processes add-on as a container too; it reads the host's processes from its `/proc`, mounted read-only, and needs no root, so it runs as the image's user, like usage-control. The gpu add-on has no container, since the image carries no `nvidia-smi`; use the Linux archive for it. The inodes add-on has no container: to ask the host's filesystems for their inodes, the container would need the host's whole filesystem mounted, readable as root, which is more than it is worth, so it comes only with the Linux archive. The smart add-on has no container: it would need the host's raw disks and capabilities to send them commands, which the containers here never get, so for it, use the Linux archive.

## 2. Add a Windows PC

Docker on Windows would measure its Linux VM, not the PC, so Windows gets an installer. From the [releases page](https://github.com/Chriszly/usage-control/releases), download `usage-control-<version>-x64.msi` for most PCs, or `-arm64.msi` for Windows on ARM, and run it. The installers are not code-signed, so SmartScreen warns before it runs.

The setup wizard asks what the PC should do:

- **Only collect this PC's usage for a hub** (preselected): the website is off (`DATA_ONLY=true`), and the PC only answers the hub's requests and keeps no history of its own, only the last 24 hours for a hub that cannot reach it, until the hub has them
- **Collect and also show the website on this PC**: the PC also shows the page and keeps its own history in `C:\ProgramData\Usage Control`

To switch later, run the installer again and pick the other one. Either way, the installer:

- installs the program in `C:\Program Files\Usage Control` as the Windows service *Usage Control*, which starts with Windows and runs under the low-privilege Local Service account
- opens port 9393 in the Windows firewall, for **private networks only**. If Windows set up the network as public, switch it to private in the Windows settings, or the hub cannot reach the PC
- adds the tray icon described below, which starts for everyone who logs in

The next page, **Add-ons**, has a box for each add-on that works on Windows, all cleared by default. The **power add-on** (`POWER=1` when installing silently), ticked, installs `usage-control-power.exe` as the service *Usage Control power add-on*, in the same Local Service account, which reads what Windows itself offers: the *Energy Meter* performance counters, which Windows 10 and 11 fill where the CPU or firmware has energy meters (Intel CPUs' RAPL counters per package, cores and memory, named as on Linux, and the meters of many laptops and Surface devices), and, on a laptop running on its battery, the power the battery gives, which is what the whole laptop draws (while it is plugged in, Windows only tells how fast the battery charges, so then there is no value). It also reads NVIDIA graphics cards through `nvidia-smi`. Where none of these exist, the add-on shows nothing. The **gpu add-on** (`GPU=1`) installs `usage-control-gpu.exe` as the service *Usage Control gpu add-on*, in the same account, which reads the fan, clocks, video encoder and decoder, performance state and power limit of an NVIDIA graphics card. The **pressure add-on** (`PRESSURE=1`) installs `usage-control-pressure.exe` as the service *Usage Control pressure add-on*, also in the Local Service account, which reads from the performance counters built into Windows how many threads wait for a processor, how many pages are read from disk for memory and how busy the disks are; Windows has no stall measurement like Linux's, so these are approximations. The **kernel add-on** (`KERNEL=1`) installs `usage-control-kernel.exe` as the service *Usage Control kernel add-on*, also in the Local Service account, which reads what Windows is busy with: context switches and interrupts per second, open handles, established TCP connections and TCP retransmissions per second. It shows handles where Linux shows open files, and it leaves out new processes and sockets, which Windows does not count. The **Wi-Fi add-on** (`WIFI=1`) installs `usage-control-wifi.exe` as the service *Usage Control Wi-Fi add-on*, also in the Local Service account, which reads the signal quality and strength of each connected Wi-Fi adapter through the WLAN API built into Windows. The **memory add-on** (`MEMORY=1`) installs `usage-control-memory.exe` as the service *Usage Control memory add-on*, also in the Local Service account, which reads the Memory performance counters Windows keeps for Performance Monitor. The **ports add-on** (`PORTS=1`) installs `usage-control-ports.exe` as the service *Usage Control ports add-on*, also as Local Service, which lists the TCP and UDP ports the PC listens on from the tables Windows keeps. The **smart add-on** (`SMART=1`) installs `usage-control-smart.exe` as the service *Usage Control smart add-on*, which reads the disks' health like on Linux, through what Windows itself offers: the NVMe driver's health log for NVMe disks, and the disk drivers' SMART commands for SATA disks. Before each read it asks Windows whether it has switched the disk off, and a SATA disk also itself whether it sleeps, through ATA pass-through where the driver passes ATA commands. Windows lets only administrators send these, so unlike the other add-ons it runs as *LocalSystem*; it only sends commands that read. Disks behind RAID drivers such as Intel RST, and NVMe disks with a vendor's own driver instead of Windows' one, may report nothing. The **processes add-on** (`PROCESSES=1`) installs `usage-control-processes.exe` as the service *Usage Control processes add-on*, also as Local Service, which lists the ten processes using the most CPU and memory; it asks Windows for all processes in one call, which sees every process, also those of other users and of the system. Running the installer again with a box cleared removes that add-on. See [Add-ons](#add-ons).

### The tray icon

The bear in the taskbar's notification area shows whether Usage Control runs:

| Dot | Meaning |
| --- | --- |
| green | the service runs and answers |
| red | the service is stopped, or runs but does not answer |
| grey | paused from the icon, or starting or stopping |

Its menu, opened with a click:

- **Address** shows where other devices reach this PC, such as `192.168.1.23:9393`: the IPv4 address of the network adapter Windows uses for its default route (else the first private one) and the installer's port, looked up again every minute. This is the address to add on the hub's *Devices* dialog
- **Open hub** opens the page of the hub that collects from this PC. The hub tells the PC its address each time it asks for the usage, so this needs no setup; until a hub has asked, the entry is greyed out
- **Open the page on this PC** appears when the PC shows the website itself (`WEBSITE=1`)
- **Pause** stops collecting until **Resume**. The service starts again with Windows
- **Stop and exit** stops the service and closes the icon until the next login. The service starts again with Windows
- **Start** appears when the service was stopped some other way

Every logged-in user may start and stop the service from the icon, without an administrator prompt; nothing else about the service changes. The menu follows the Windows display language (English, German, French or Spanish). The setup wizard's last page shows the icon right away; after a silent install it appears at the next login.

To change the other defaults, or to install without the wizard (`/qn`), install from a command prompt run as administrator and add options:

```bat
msiexec /i usage-control-<version>-x64.msi PORT=8090 WEBSITE=1 RETENTION_DAYS=90
```

| Option | Meaning |
| --- | --- |
| `PORT` | port the PC is reachable on (default 9393) |
| `WEBSITE` | the wizard's choice: `1` to show the website on this PC too and keep its history in `C:\ProgramData\Usage Control`, which uninstalling keeps; `0` to only collect (default `0`) |
| `DEVICE_NAME` | with `WEBSITE=1`, how the page names this PC (default *Host Hub*) |
| `HUB_DEVICES` | with `WEBSITE=1`, other devices this PC collects from |
| `RETENTION_DAYS` | with `WEBSITE=1`, days of history to keep (default 30) |
| `HISTORY_MAX_ENTRIES` | with `WEBSITE=1`, how many disks, sensors, network cards and GPUs each the history keeps per device (default 64) |
| `BUFFER_HOURS` | without `WEBSITE=1`, hours of usage kept for a hub that cannot reach this PC, from 1 to 168 (default 24) |
| `ALLOWED_HOSTS` | other names this PC answers to, such as a name from the router's DNS the hub uses for it |

An update closes the tray icon while it replaces it; it comes back at the next login, or right away when the update ran through the wizard. An update keeps the options it was installed with, so double-clicking a newer installer is enough; options given to the update replace the old ones. The options are remembered under `HKLM\SOFTWARE\Usage Control`.

The service writes errors to the Windows event log (*Application*, source *UsageControl*). When it cannot serve, for example because another program holds the port right after a reboot, it keeps running and tries again every 10 seconds.

Builds of `main` are also available, as artifacts of the *Windows installer* workflow runs (pull requests only build the installers when they change them). They are numbered `0.0.<run>`, which counts as older than any release, so they only install on a PC without a release.

## 3. Add another Linux machine

Install it as in [step 1](#1-set-up-the-hub-with-docker) or [without Docker](#or-set-up-a-linux-machine-without-docker). If it should only report to the hub, turn its own website and history off in `.env` (Docker) or `/etc/usage-control.env` (service):

```bash
DATA_ONLY=true
```

Leaving its website on does no harm: the hub only reads its usage, and the machine then also keeps its own history and page.

## 4. Add the devices on the hub

On the hub's page, click *Devices*, then add each device with a name and its address and port, such as *Office PC* at `192.168.1.30:9393`. The hub checks that a usage-control answers there before it adds the device, and starts collecting right away, with no restart. Each address and port can be added only once; the same IP address with another port counts as another device.

Pick what each device is:

- *Server / IoT* for a device that should run all the time, such as a NAS or a Pi. When it does not answer, that is an outage, and its card is called *Availability*.
- *PC / laptop* for a device that is switched off when it is not used. The time it is off is not an outage: its card is called *Usage* and shows how much of the time it was on, and its button gets a grey dot instead of a red one while it is off.

The kind can be changed later in the list, for devices from `HUB_DEVICES` too; that asks for the password as well. Devices added before there were kinds, and the ones in `HUB_DEVICES`, are servers until changed.

Opened from a device that is not added yet, the dialog fills in that device's address with port 9393, its kind (a PC when it has a battery or runs Windows), and its name when the hub can find it out: the `DEVICE_NAME` or hostname that its usage-control reports, or else the name the router gives it in the local DNS. So the quickest way to add a PC is to open the hub's page on that PC. Change the port if the device listens on another one.

The first device you add asks you to choose a password, at least 8 characters, typed twice. From then on, adding or removing a device or changing its kind asks for it; it cannot be changed on the page. If it is forgotten, set `RESET_PASSWORD=true` on the hub, restart it, and unset it again right away: while it is set, every restart deletes the password, and whoever next adds or removes a device chooses the new one.

Removing a device deletes its history too, unless *Keep its history* is ticked. The history is kept under the device's name, so renaming a device (removing it and adding it under a new name) starts a new history.

Instead of the dialog, devices can be listed on the hub in `HUB_DEVICES`, each as `name=address:port`, separated by commas. They show as *Set in .env* and are removed only there:

```bash
HUB_DEVICES=Living room Pi=192.168.1.20:9393,Office PC=192.168.1.30:9393
```

Give the devices fixed addresses, for example with an address reservation in the router, since the hub reaches them by the address it was given. A host name works too, as long as it resolves to an address on the local network. The device must then answer to that name: a `.local` name or its own hostname always works, any other name needs the device's `ALLOWED_HOSTS`.

Buttons above the dashboard switch between the devices. A dot on each shows whether it answers (green) or not (red, with the time it stopped answering on hover). `DEVICE_NAME` sets how the page names the hub itself (default *Host Hub*).

## Run from source

To run usage-control on a computer without Docker or an installer, such as a Mac, build the website once and then the binary. You need Node.js (version in `frontend/.nvmrc`) and Go (version in `backend/go.mod`). In PowerShell on Windows:

```powershell
cd frontend; npm ci; npm run build; cd ..
cd backend; go build -o usage-control.exe ./cmd/usage-control
$env:DEVICE_NAME = "Office PC"   # optional
.\usage-control.exe
```

On Linux and macOS, build with `go build -o usage-control ./cmd/usage-control` and start it with `DEVICE_NAME="Office PC" ./usage-control`. The page is then at <http://localhost:9393>, and the history is kept in `usage-control.db` in the folder it was started from. Add `DATA_ONLY=true` to only report to a hub.

On Windows, allow `usage-control.exe` on private networks when the firewall asks on the first start. Start the built binary rather than `go run`, which builds a new file each time, so the firewall would ask again.

## Settings

Settings are environment variables. In Docker they go in `.env` next to `compose.yaml`; for the Linux service in `/etc/usage-control.env`; on Windows they are installer options (see [step 2](#2-add-a-windows-pc)). A setting takes effect at the next start. An invalid value stops the program with a message that names the setting.

| Setting | Default | Meaning |
| --- | --- | --- |
| `PORT` | `9393` | Docker only: the port on the host. The container always listens on 9393 inside |
| `IMAGE_TAG` | `latest` | Docker only: `latest`, a release such as `0.1.0`, or `main` |
| `LISTEN_ADDR` | `:9393` | outside Docker: the address and port to listen on, such as `:8090` |
| `PUBLIC_PORT` | the port of `LISTEN_ADDR`; `PORT` in Docker | the port the page is reachable on from the network. A hub sends it to the devices it collects from, so a Windows PC can link to the hub's page |
| `DISK_PATHS` | `/`, or the system drive on Windows | comma-separated paths whose disks are shown; in Docker, mount each one read-only first |
| `DATABASE_PATH` | `usage-control.db` in the working folder; set by Docker, the Linux service and the Windows installer | the SQLite file for the history, or with `DATA_ONLY` for the usage kept for the hub |
| `RETENTION_DAYS` | `30` | days of history to keep, 1 to 3650 |
| `HISTORY_MAX_ENTRIES` | `64` | disks, sensors, network cards and GPUs each that the history keeps per device, and 8 times as many values of extras, 1 to 10000 |
| `DEVICE_NAME` | *Host Hub* | how the page names this device |
| `HUB_DEVICES` | none | other devices to collect from, as `name=address:port`, comma-separated |
| `DATA_ONLY` | `false` | `true` serves only the usage for a hub, with no website and no history of its own |
| `BUFFER_HOURS` | `24` | with `DATA_ONLY`, hours of usage kept for a hub that cannot reach the device, 1 to 168; see [While the hub is away](architecture.md#while-the-hub-is-away) |
| `RESET_PASSWORD` | `false` | `true` deletes the password for changing devices at start; unset it again right after |
| `UPDATE_CHECK` | `true` | `false` stops the daily check for a newer release |
| `ALLOWED_HOSTS` | none | other names this device answers to, comma-separated, besides IP addresses, `localhost`, its hostname and `.local` names |
| `ADDONS_DIR` | `/run/usage-control-addons` for the Linux service; else none | the folder add-ons write their values to, which are shown as extras |
| `HOST_PROC`, `HOST_SYS` | set by `compose.yaml` | where the host's `/proc` and `/sys` are mounted in a container |

## Updating

**Docker**, in the folder with `compose.yaml`:

```bash
git pull               # when compose.yaml itself changed
docker compose pull
docker compose up -d
```

**Linux service:** unpack the newer archive and run `sudo ./install.sh` again.

**Windows:** run the newer installer; it keeps its options.

The history and settings stay in every case. What changed is on the [releases page](https://github.com/Chriszly/usage-control/releases). When a new version changes the database layout, it keeps a copy of the old file first ([details](database.md#updates-to-the-layout)).

On a release, the hub checks GitHub once a day for a newer one, without an account, and the page names it with a link. Nothing is downloaded or installed; updating stays your step. Builds of `main` and data-only devices never check. `UPDATE_CHECK=false` turns it off.

**Port change after 0.1.0:** version 0.1.0 and builds before the change listened on 8080. Existing installs keep their port: a Docker install that set `PORT` in `.env`, a Windows install, which remembers the port it was installed with, and a Linux service with `LISTEN_ADDR` in `/etc/usage-control.env`. A Docker install that never set `PORT` moves to 9393 after `git pull` and `docker compose up -d`; set `PORT=8080` in `.env` to stay on the old port, or update the address on the hub's *Devices* dialog.

Deploying to the maintainer's Pi lives in the private settings repository rpi-deploy-and-update (its *Deploy to Pi* and *Update Pi* buttons); this repository only builds and publishes the image.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| A device shows a red dot | The hub has had no answer for 20 seconds. Check that the device is on and usage-control runs on it (`docker compose ps`, `systemctl status usage-control`, or the *Usage Control* service on Windows), and that the address and port on the hub are right |
| A red banner says the Host Hub is not answering, and the other devices have hollow dots | The page lost its connection to the hub, which all values come through, so nothing on it is live. Check that the hub is on and usage-control runs on it (`docker compose ps` or `systemctl status usage-control`), and that your phone or PC is still on the same network. The page keeps trying and clears the banner by itself as soon as the hub answers |
| A Windows PC cannot be added or stays red | The network is set to public, so the firewall blocks the port. Switch it to private in the Windows settings |
| "no usage-control answers at …" when adding | Nothing answered at that address within 4 seconds. Open `http://<address>/api/metrics` from the hub's network to check |
| `403 only reachable from the local network` | The request came from outside the local network, or through a proxy. Open the page directly from the LAN. In Docker, check that the port is published as `0.0.0.0:…` (`ss -tlnp` on the host) |
| `421 Misdirected Request` | The page was opened by a name the device does not know. Use the IP address or a `.local` name, or add the name to `ALLOWED_HOSTS` |
| Temperature shows as unavailable | The system does not report it to programs, as on most Windows PCs and in containers without the host's `/sys` |
| No GPU card | No GPU the system reports without extra rights; see [GPUs](data.md#gpus) |
| The program does not start: "check DISK_PATHS" | A path in `DISK_PATHS` cannot be read. In Docker, mount it read-only in `compose.yaml` first |
| The program does not start: "the database was written by a newer version" | An older version was started on a newer database. Start the newer version again, or put the `.backup` copy back ([details](database.md#updates-to-the-layout)) |
| Some disks, sensors or network cards are missing from the charts | The device reports more than `HISTORY_MAX_ENTRIES` (64) of them; the log says so. Raise it on the hub |

Logs: `docker compose logs` for Docker, `journalctl -u usage-control` for the Linux service, and the Windows event log (*Application*, source *UsageControl*) on Windows.
