# usage-control

A website that shows the usage of the hardware it runs on (CPU, memory, disk, temperature and so on).

It is built with a Go backend, an Angular frontend and a SQLite file for the history, and runs first on Linux (starting with a Raspberry Pi) with Docker. A native Windows installer comes later. See [AGENTS.md](AGENTS.md#tech-stack) for the details.

It shows CPU, memory and disk usage, network speed per network card, temperature sensors and uptime so far, refreshed every two seconds, and is only reachable from the local network. Below that, charts show the usage over the last 1 minute up to 30 days, or all of the kept history.

The page is in English, German, French and Spanish. It opens in the browser's language, and the flag buttons at the top switch to another one.

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

The site reads the machine's usage every 5 seconds and keeps the last 30 minutes of readings in memory, for the short ranges. Every minute it stores their average in a SQLite file on the `data` Docker volume, so it survives updates. It keeps 30 days by default and deletes older values once a day, and when it starts. To keep it longer or shorter, set the number of days in `.env` and restart the container:

```bash
RETENTION_DAYS=90
```

With more than 30 days, the page also offers an *All* range covering everything kept. Outside Docker, the file is `usage-control.db` in the working folder; `DATABASE_PATH` moves it.

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

### Translations

The website uses Angular's built-in translations (`@angular/localize`). English is the source language; `npm run build` builds the website once per language into its own folder (`/en/`, `/de/`, `/fr/`, `/es/`), and the backend sends a visit to `/` on to the language picked with the page's flag buttons, else the browser's language, else English.

- Mark new text in a template with `i18n="@@area.name"` (or `i18n-aria-label` for an attribute) and in TypeScript with `` $localize`:@@area.name:Text` ``
- Run `npm run extract-i18n` to update `src/locale/messages.json`, then add the same keys to `messages.de.json`, `messages.fr.json` and `messages.es.json`. The build fails when a translation is missing
- `npm start -- --configuration=de` runs the live-reload website in German (or `fr`, `es`)

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
(cd frontend && npm run format:check && npm run lint && npm audit --audit-level=high && npm test && npm run build && npm run extract-i18n && git diff --exit-code src/locale/messages.json)
```

- Formatting: `gofumpt` and `goimports` for Go (`golangci-lint fmt` fixes it), Prettier for the frontend (`npm run format` fixes it)
- Linting: `golangci-lint` with the settings in `backend/.golangci.yml`; ESLint in the frontend, which also fails on circular imports
- Dependencies: `govulncheck` and `npm audit` fail on known vulnerabilities; `go mod tidy -diff` fails when `go.mod` or `go.sum` is out of date
