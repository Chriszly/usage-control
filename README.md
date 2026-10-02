# usage-control

A website that shows the usage of the hardware it runs on (CPU, memory, disk, temperature and so on).

It is built with a Go backend, an Angular frontend and a SQLite file for the history, and runs first on Linux (starting with a Raspberry Pi) with Docker. A native Windows installer comes later. See [AGENTS.md](AGENTS.md#tech-stack) for the details.

It shows CPU, memory and disk usage, network speed per network card, temperature sensors and uptime so far, refreshed every two seconds, and is only reachable from the local network. Below that, charts show the history, which you can scroll back through as far as it is kept.

## Run it with Docker (Linux, Raspberry Pi)

```bash
cp .env.example .env   # optional, to change the port or the image version
docker compose pull
docker compose up -d
```

This runs the published image `ghcr.io/chriszly/usage-control` for arm64 and amd64, so the Pi doesn't build anything. `IMAGE_TAG` picks the version: `main` (default) follows the main branch, and a release such as `1.2.3` stays fixed. To build the image from this checkout instead, run `docker compose up -d --build`.

Then open `http://<the machine's address>:8080` from a device on the same network. The container reads the host's `/proc` and `/sys` read-only, runs as a non-root user and has no extra privileges.

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

Every minute the site stores the machine's usage in a SQLite file on the `data` Docker volume, so it survives updates. It keeps 30 days by default and deletes older values automatically. To keep it longer or shorter, set the number of days in `.env` and restart the container:

```bash
RETENTION_DAYS=90
```

The page can scroll back as far as `RETENTION_DAYS`. Outside Docker, the file is `usage-control.db` in the working folder; `DATABASE_PATH` moves it.

## Deploy to a Raspberry Pi

Deploying to the Pi lives in the private settings repository rpi-deploy-and-update (its *Deploy to Pi* and *Update Pi* buttons). This repository only builds and publishes the image (`.github/workflows/docker.yml`).

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
