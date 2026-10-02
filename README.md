# usage-control

A website that shows the usage of the hardware it runs on (CPU, memory, disk, temperature and so on).

It is built with a Go backend and an Angular frontend (storing history in SQLite comes next), and runs first on Linux (starting with a Raspberry Pi) with Docker. A native Windows installer comes later. See [AGENTS.md](AGENTS.md#tech-stack) for the details.

It shows CPU and memory usage, temperature sensors and uptime so far, refreshed every two seconds, and is only reachable from the local network.

## Run it with Docker (Linux, Raspberry Pi)

```bash
cp .env.example .env   # optional, to change the port or the image version
docker compose pull
docker compose up -d
```

This runs the published image `ghcr.io/chriszly/usage-control` for arm64 and amd64, so the Pi doesn't build anything. `IMAGE_TAG` picks the version: `main` (default) follows the main branch, and a release such as `1.2.3` stays fixed. To build the image from this checkout instead, run `docker compose up -d --build`.

Then open `http://<the machine's address>:8080` from a device on the same network. The container reads the host's `/proc` and `/sys` read-only, runs as a non-root user and has no extra privileges.

## Deploy from GitHub (Deploy to Pi button)

**Actions > Deploy to Pi > Run workflow** deploys the published image to the Pi: it copies `compose.yaml` to `~/usage-control` on the Pi, pulls the image (`image_tag`: `main` or a version), starts the container on `port` and checks that the website answers. It runs on a GitHub Actions runner on the Pi, so nothing connects in to the Pi and GitHub stores no SSH key or Pi address. The job logs in to the registry with its own short-lived token, so the image may stay private.

One-time setup on the Pi (needs Docker; rpi-setup's `docker` task installs it):

1. In this repository open **Settings > Actions > Runners > New self-hosted runner**, pick Linux and ARM64, and keep the page open.
2. On the Pi, create a user for the runner that may use Docker:

   ```bash
   sudo useradd --system --create-home --shell /usr/sbin/nologin --groups docker usage-runner
   sudo -u usage-runner -s   # continue as that user
   cd ~
   ```

3. Run the page's **Download** commands, then its `./config.sh` command with `--labels pi5 --unattended` added, and leave the shell (`exit`).
4. Run the runner as a service: `cd /home/usage-runner/actions-runner && sudo ./svc.sh install usage-runner && sudo ./svc.sh start`.

The runner then shows as *Idle* under Settings > Actions > Runners, and **pi** `pi5` in the workflow picks it. This runner is separate from the one rpi-setup installs for your settings repository: a runner serves one repository. Being in the `docker` group gives `usage-runner` root rights on the Pi, so anyone who can run workflows in this repository can change the Pi; keep the repository private.

## Deploy to a Raspberry Pi from your PC

`scripts/deploy.sh` does the steps above on the Pi over SSH: it copies `compose.yaml` and your `.env` there, pulls the published image and starts it. The Pi needs Docker with the compose plugin, key login over SSH, and the SSH user in the `docker` group (rpi-setup's `docker` task sets these up).

```bash
cp .env.example .env               # fill in PI_HOST; .env is git-ignored
bash scripts/deploy.sh --dry-run   # show what it would run
bash scripts/deploy.sh
```

A variable on the command line wins over `.env`, e.g. `PI_HOST=pi4.local IMAGE_TAG=1.2.3 bash scripts/deploy.sh`. Running it again pulls the newest image and recreates the container only when it changed.

## Develop

| Part | Folder | Toolchain |
| --- | --- | --- |
| Backend and API | `backend/` | Go (version in `backend/go.mod`) |
| Website | `frontend/` | Angular, Node.js LTS (version in `frontend/.nvmrc`) |

```bash
# Terminal 1: the backend, serving the API on http://localhost:8080
cd backend && go run ./cmd/usage-control

# Terminal 2: the website with live reload on http://localhost:4200, forwarding /api to the backend
cd frontend && npm ci && npm start
```

`npm run build` in `frontend/` writes the website into `backend/internal/web/files/build/`, and `go build` embeds it, so one binary serves both.

## Repository

- [AGENTS.md](AGENTS.md): code style, security, testing and pull request rules (also read by AI agents through `CLAUDE.md`)
- [AGENTS.md](AGENTS.md): code style, security, testing and pull request rules (also read by AI agents through `CLAUDE.md`)
- [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md): every pull request fills it in; the "PR Template Validation" workflow checks it
- `ci/`: checks that CI runs and that you can run locally
- `scripts/deploy.sh`: deploys to a Raspberry Pi over SSH
- `.github/workflows/deploy.yml`: the Deploy to Pi button, on a runner on the Pi
- [SECURITY.md](SECURITY.md): how to report a vulnerability and who can change the code

## Checks

Run before opening a pull request:

```bash
bash ci/check-no-secrets.sh
(cd backend && gofmt -l . && golangci-lint run && go test ./...)
(cd frontend && npm run format:check && npm run lint && npm test && npm run build)
```
