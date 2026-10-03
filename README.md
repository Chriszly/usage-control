# usage-control

A website that shows the usage of the hardware it runs on (CPU, memory, disk, temperature and so on).

It is built with a Go backend, an Angular frontend and a SQLite file for the history, and runs on Linux (starting with a Raspberry Pi) with Docker, and on Windows with an installer. See [AGENTS.md](AGENTS.md#tech-stack) for the details.

It shows CPU usage (also per core, with clock and load average), memory and swap, disk usage and read/write speed, GPU usage, network speed per network card, temperature sensors, a Raspberry Pi's undervoltage and throttling warnings, battery charge and uptime so far, refreshed every two seconds, and is only reachable from the local network. Below that, charts show the usage over the last 1 minute up to 30 days, or all of the kept history. One device can also collect from the others on the network and show them all ([hub mode](#several-devices-hub-mode)).

The page is in English, German, French and Spanish. It opens in the browser's language, and the flag buttons at the top switch to another one in place, without reloading the page.

## Run it with Docker (Linux, Raspberry Pi)

```bash
cp .env.example .env   # optional, to change the port or the image version
docker compose pull
docker compose up -d
```

This runs the published image `ghcr.io/chriszly/usage-control` for arm64 and amd64, so the Pi doesn't build anything. `IMAGE_TAG` picks the version: `latest` (default) is the newest release, a release such as `0.1.0` stays fixed, and `main` follows every change on the main branch, for testing. To build the image from this checkout instead, run `docker compose up -d --build`.

Then open `http://<the machine's address>:9393` from a device on the same network. The container reads the host's `/proc` and `/sys` read-only, runs as a non-root user and has no extra privileges. The port is published over IPv4 only, so the program sees each client's real address and can keep requests from outside the local network out; a browser that tries IPv6 first falls back to IPv4 by itself.

The page answers only requests from the local network: private, link-local and loopback addresses, and IPv6 addresses in one of the machine's own subnets. It also answers only when opened by a name it knows: an IP address, `localhost`, its hostname or a `.local` name. A page from the internet could otherwise point its domain at the machine's LAN address and read the API from a browser on the LAN (DNS rebinding). To open the page by another name, such as one from the router's DNS, list it in `.env`:

```bash
ALLOWED_HOSTS=pi.fritz.box
```

Inside Docker the hostname is the container's, so a bare `http://raspberrypi:9393` needs that entry too; `raspberrypi.local` does not.

### Disks

By default the page shows the disk Docker keeps its data on, usually the system disk. The container sees no other host folder unless you mount it. To show another disk, such as a USB disk mounted at `/mnt/usb`, mount it read-only at the same path in `compose.yaml` and list it in `.env`:

```yaml
    volumes:
      - /mnt/usb:/mnt/usb:ro
```

```bash
DISK_PATHS=/,/mnt/usb
```

The site refuses to start when a path in `DISK_PATHS` cannot be read, and says which one.

### History

The site reads the machine's usage every 5 seconds and keeps the last 30 minutes of readings in memory, for the short ranges. Every minute it stores their average in a SQLite file on the `data` Docker volume, so it survives updates, and keeps an average per hour next to it, which the charts of 30 days and more are drawn from. It keeps 30 days by default and deletes older values once a day, and when it starts. To keep it longer or shorter, set the number of days in `.env` and restart the container:

```bash
RETENTION_DAYS=90
```

With more than 30 days, the page also offers an *All* range covering everything kept. Outside Docker, the file is `usage-control.db` in the working folder; `DATABASE_PATH` moves it.

When a new version changes the layout of the database, it first copies the file to `usage-control.db.backup` next to it, then updates it. An older version refuses to start on a database a newer one has changed, instead of damaging it; to go back, put the old version and the `.backup` copy back in place.

### Several devices (hub mode)

Every device runs the same image. On its own it only shows itself. To see several devices on one page, open the page of the device that should be the hub, click *Add other devices* (later *Devices*) and add each other device with a name and its address, such as `192.168.1.30:9393`. The hub checks that a usage-control answers there before it adds the device, and starts collecting right away, with no restart.

The first device you add asks you to choose a password, at least 8 characters, repeated once. From then on, adding or removing a device asks for it, and it cannot be changed on the page. Only a salted hash is kept, in the hub's database. If it is forgotten, set `RESET_PASSWORD=true` in the hub's `.env`, restart it, and unset it again: the next device you add chooses a new password. Removing a device deletes its history too, unless you tick *Keep its history*.

Devices can also be listed in the hub's `.env`, each as `name=address:port`, separated by commas. These show on the page as *Set in .env* and are removed only there:

```bash
HUB_DEVICES=Living room Pi=192.168.1.20:9393,Office PC=192.168.1.30:9393
```

`DEVICE_NAME` optionally sets how the page names the hub itself (default *Host Hub*). A device that only reports to the hub can turn its own website and history off with `DATA_ONLY=true` in its `.env`, as the [Windows installer](#install-it-on-windows) does.

The hub asks each device for its usage every 5 seconds and keeps their history in its own database, with the same retention, so the other devices need no setting. Buttons above the dashboard switch between the devices; a dot on each shows whether it answers (green) or not (red, with the time it stopped answering on hover). A device that does not answer is shown as such, and its history has a gap for that time. Its *Availability* card shows the share of time it answered since it was added, how long it was offline in total, and its last outage. Time the hub itself was not running is not counted as offline. The hub only connects to addresses on the local network. The history is kept under the device's name, so renaming a device starts a new history.

### Updating

To update to the newest release, run in the folder with `compose.yaml`:

```bash
docker compose pull
docker compose up -d
```

The history stays on the `data` volume. When `compose.yaml` itself changed, update the checkout too (`git pull`) before `docker compose up -d`. What changed is on the [releases page](https://github.com/Chriszly/usage-control/releases). On Windows, run the newer installer from that page; it keeps the options it was installed with. Builds of main number their installers `0.0.<run>`, which counts as older than any release, so a main installer only installs on a PC that has no release installed.

### New versions

Once a day the hub asks GitHub's public releases API whether a newer release exists, without an account, and the page then names it with a link to what changed. Nothing is downloaded or installed; updating stays your step. Only releases check, not builds of `main`, and devices with `DATA_ONLY=true` never do. To turn the check off, set in `.env`:

```bash
UPDATE_CHECK=false
```

## Install it on Linux without Docker

Each [release](https://github.com/Chriszly/usage-control/releases) also has an archive for Linux machines without Docker: `usage-control-<version>-linux-arm64.tar.gz` for a Raspberry Pi with a 64-bit system, `-amd64.tar.gz` for a PC. It holds the program, a systemd service and an install script:

```bash
tar -xzf usage-control-0.1.0-linux-arm64.tar.gz
cd usage-control-0.1.0-linux-arm64
sudo ./install.sh
```

The service starts right away and at every boot, on port 9393. Settings go in `/etc/usage-control.env` (the same ones as in `.env` for Docker), followed by `sudo systemctl restart usage-control`. The history is kept in `/var/lib/usage-control`. Running natively, it sees every disk and needs no `HOST_PROC` or mounts. Like the container, the service runs as an unprivileged throwaway user that can write only its own folder.

To update, unpack the newer archive and run its `install.sh` again; settings and history are kept. `sudo ./install.sh --uninstall` removes it, and `--uninstall --purge` also deletes its settings and history.

## Install it on Windows

On Windows, usage-control by default only collects the PC's usage for a hub on Linux, such as a Raspberry Pi, which shows it and keeps its history. Docker on Windows would measure its Linux VM, not the PC, so Windows gets an installer instead: `usage-control-<version>-x64.msi` for most PCs, or `-arm64.msi` for Windows on ARM. Version tags attach both to the [GitHub release](https://github.com/Chriszly/usage-control/releases); every pull request and every commit on main also builds them, under the run's artifacts in the *Windows installer* workflow.

Running the installer:

- installs the program in `C:\Program Files\Usage Control` as the Windows service *Usage Control*, which starts with Windows and runs under the low-privilege Local Service account
- turns the website off (`DATA_ONLY=true`): the PC only answers the hub's `/api/metrics` requests and keeps no history of its own, unless it is installed with `WEBSITE=1`
- opens port 9393 in the Windows firewall, for private networks only. If Windows set up the network as public, switch it to private in the Windows settings, or the hub cannot reach the PC

Then add the PC on the hub's page, such as *Office PC* at `192.168.1.30:9393`.

To change the defaults, install from a command prompt run as administrator and add options:

```bat
msiexec /i usage-control-1.2.3-x64.msi PORT=8090 WEBSITE=1 RETENTION_DAYS=90
```

| Option | Meaning |
| --- | --- |
| `PORT` | Port the PC is reachable on (default 9393) |
| `WEBSITE` | `1` to show the website on this PC too and keep its history in `C:\ProgramData\Usage Control`, which uninstalling keeps; `0` to turn it off again (default off) |
| `DEVICE_NAME` | With `WEBSITE=1`, how the page names this PC (default *Host Hub*) |
| `HUB_DEVICES` | With `WEBSITE=1`, other devices this PC collects from, as on Linux ([hub mode](#several-devices-hub-mode)) |
| `RETENTION_DAYS` | With `WEBSITE=1`, days of history to keep (default 30) |
| `ALLOWED_HOSTS` | Other names this PC answers to, besides its IP addresses, `localhost`, its hostname and `.local` names; comma-separated, for a hub that lists the PC by a name from the router's DNS |

An update keeps the options it was installed with, so double-clicking a newer installer is enough; options given to the update replace the old ones.

Windows often does not tell programs the temperature, so the page shows it as unavailable. The service writes errors to the Windows event log (*Application*, source *UsageControl*).

### GPUs

The GPU card appears when usage-control finds a GPU whose usage the system reports to programs without extra rights:

- **Windows:** every GPU, through the counters Task Manager shows, with its own memory. Windows has no GPU temperature for programs, so it is left out
- **Linux:** AMD GPUs with usage, memory and temperature, and the Raspberry Pi's VideoCore GPU with usage, read from `/sys`. The Pi's GPU shares the main memory and its temperature is the Pi's CPU temperature, so only usage is shown. Older Raspberry Pi kernels do not report it, then the card stays hidden. NVIDIA GPUs show up when `nvidia-smi` is installed, which is not the case in the Docker image. Intel GPUs report their usage only to programs with extra rights, so they are not shown
- **macOS:** not shown

### What each system reports

Every value is a small read the system offers to programs without extra rights, taken every few seconds. Values a system does not report are left out of the page:

| Value | Linux | Windows | Kept in the history |
| --- | --- | --- | --- |
| Usage per core | yes | yes | no, only the total |
| CPU clock | where the kernel scales it (not in most virtual machines) | no | no |
| Time and time zone | yes; in Docker, the host's zone when `/etc/localtime` is mounted (as in `compose.yaml`), else UTC | yes | no |
| Load average | yes | no, Windows has none | no |
| Processes (total and running) | yes | no | no |
| I/O wait and steal | yes | no | no |
| Available memory and cache | both | available only | no |
| Swap | when a swap file or partition exists | page files | yes |
| Disk read/write speed | disks and partitions; for `/` inside Docker, the host's root disk | per drive letter | yes |
| Disk operations per second | yes | yes | no |
| Disk busy % and time per operation | yes | no | no |
| Network errors and dropped packets | since start | since start | no |
| Network link speed and IPv4 address | yes, read once a minute; most Wi-Fi cards report no speed | yes, read once a minute | no |
| Fan speed | where the kernel knows the fan, such as the Raspberry Pi 5 | no | no |
| Undervoltage and throttling | Raspberry Pi only | no | no |
| Battery charge and plugged in | laptops and tablets | laptops and tablets | yes, the charge |
| Battery power (W) and health | where the battery reports them | no | no |

## Deploy to a Raspberry Pi

Deploying to the Pi lives in the private settings repository rpi-deploy-and-update (its *Deploy to Pi* and *Update Pi* buttons). This repository only builds and publishes the image (`.github/workflows/docker.yml`).

## Develop

| Part | Folder | Toolchain |
| --- | --- | --- |
| Backend and API | `backend/` | Go (version in `backend/go.mod`) |
| Website | `frontend/` | Angular, Node.js LTS (version in `frontend/.nvmrc`) |

```bash
# Terminal 1: the backend, serving the API on http://localhost:9393
cd backend && go run ./cmd/usage-control

# Terminal 2: the website with live reload on http://localhost:4200, forwarding /api to the backend
cd frontend && npm ci && npm start
```

`npm run build` in `frontend/` writes the website into `backend/internal/web/files/build/`, and `go build` embeds it, so one binary serves both.

### Run from source

To run usage-control on a computer without Docker or the installer, such as a PC that a hub on a Raspberry Pi should collect from, build the website once and then the binary. In PowerShell on Windows:

```powershell
cd frontend; npm ci; npm run build; cd ..
cd backend; go build -o usage-control.exe ./cmd/usage-control
$env:DEVICE_NAME = "Office PC"   # optional, see the settings below
.\usage-control.exe
```

On Linux and macOS, build with `go build -o usage-control ./cmd/usage-control` and start it with `DEVICE_NAME="Office PC" ./usage-control`. The page is then at <http://localhost:9393>, and the history is kept in `usage-control.db` in the folder it was started from.

The settings are environment variables, listed at the top of `backend/cmd/usage-control/main.go`. The ones that matter most outside Docker:

- `LISTEN_ADDR`: the port, such as `:8090` (default `:9393`)
- `DATABASE_PATH`: where the history is kept (default `usage-control.db` in the current folder)
- `DATA_ONLY=true`: serve only the usage data for a hub, with no website and no history, like the Windows installer does
- `ALLOWED_HOSTS`: other names this computer answers to, besides its IP addresses, `localhost`, its hostname and `.local` names

To show this computer on a hub, open the hub's page, click *Devices* and add it with its address and port, such as `192.168.1.30:9393`. The hub and this computer must be on the same local network: usage-control answers only private, link-local and loopback addresses, and IPv6 addresses in one of its own subnets. On Windows, allow `usage-control.exe` on private networks when the firewall asks on the first start. Start the built binary rather than `go run`, which builds a new file each time, so the firewall asks again.

### Translations

The website's text lives in `frontend/src/app/i18n/messages/`: `en.ts` is the source, and `de.ts`, `fr.ts` and `es.ts` must have the same keys, which the TypeScript compiler checks, so the build fails when a translation is missing. The `I18n` service holds the page's language as a signal, so switching it updates the page in place. The page starts in the language picked last time (kept in the browser's localStorage), else the browser's language, else English.

- Add a text to `en.ts` and the same key to the other three files. A `{name}` in a text is a placeholder
- Show it with `i18n.t('area.name')` (or `i18n.t('area.name', { name: value })`) in a template or a `computed`, after `protected readonly i18n = inject(I18n)`
- Format numbers and dates in the page's language by passing it to the pipe: `value | number: '1.0-1' : i18n.language()`, `time | date: format : undefined : i18n.language()`, `bytes | bytes: i18n.language()`

## Repository

- [AGENTS.md](AGENTS.md): code style, security, testing and pull request rules (also read by AI agents through `CLAUDE.md`)
- [AGENTS.md](AGENTS.md): code style, security, testing and pull request rules (also read by AI agents through `CLAUDE.md`)
- [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md): every pull request fills it in; the "PR Template Validation" workflow checks it
- `ci/`: checks that CI runs and that you can run locally
- [SECURITY.md](SECURITY.md): how to report a vulnerability and who can change the code

## Checks

Run before opening a pull request:

```bash
bash ci/check-no-secrets.sh
(cd backend && gofmt -l . && golangci-lint run && go test ./... && go mod tidy -diff && go tool govulncheck ./...)
(cd frontend && npm run format:check && npm run lint && npm audit --audit-level=high && npm test && npm run build)
```

- Formatting: `gofumpt` and `goimports` for Go (`golangci-lint fmt` fixes it), Prettier for the frontend (`npm run format` fixes it)
- Linting: `golangci-lint` with the settings in `backend/.golangci.yml`; ESLint in the frontend, which also fails on circular imports
- Dependencies: `govulncheck` and `npm audit` fail on known vulnerabilities; `go mod tidy -diff` fails when `go.mod` or `go.sum` is out of date
