# Development

How the code is laid out, how to run it while working on it, and the checks a pull request has to pass. The rules for contributions are in [AGENTS.md](../AGENTS.md).

## Layout

| Part | Folder | Toolchain |
| --- | --- | --- |
| Backend and API | `backend/` | Go (version in `backend/go.mod`) |
| Website | `frontend/` | Angular, Node.js LTS (version in `frontend/.nvmrc`) |
| Docker image | `Dockerfile`, `compose.yaml` | Docker Buildx, multi-arch |
| Linux archive | `linux/` | `install.sh`, systemd unit, settings example |
| Windows installer | `windows/` | WiX 5 |
| CI | `.github/workflows/`, `ci/` | GitHub Actions |

Backend packages, in `backend/internal/`:

| Package | Does |
| --- | --- |
| `metrics` | reads the machine's usage (`Collector`) and shares one reading every 2 s (`Sampler`) |
| `history` | the SQLite store, recorder, in-memory recent readings, pruner, migrations |
| `hub` | collects from other devices: agents, recorders, device list, availability |
| `server` | the HTTP handlers, the local-network and Host checks, the website files |
| `lan` | decides which addresses belong to the local network |
| `password` | the password for changing devices |
| `update` | the daily check for a newer release |
| `version` | the version, set at build time |
| `web` | the built website, embedded into the binary |
| `sysfile` | reads the small one-value files under `/sys` and `/proc`, for `metrics` and the add-ons |

`backend/cmd/usage-control/main.go` reads the settings and wires these together; on Windows `service_windows.go` runs it as a service. `backend/cmd/usage-control-tray` is the Windows tray icon, and `backend/cmd/tray-icons` draws its icons. How the parts work together is in [How it works](architecture.md).

## Run it while developing

```bash
# Terminal 1: the backend, serving the API on http://localhost:9393
cd backend && go run ./cmd/usage-control

# Terminal 2: the website with live reload on http://localhost:4200, forwarding /api to the backend
cd frontend && npm ci && npm start
```

`npm run build` in `frontend/` writes the website into `backend/internal/web/files/build/`, and `go build` embeds it, so one binary serves both. The website is never built on the Raspberry Pi: CI and the Docker build do it, and the Dockerfile cross-compiles the binary for arm64.

## Demo page

`npm run build:demo` in `frontend/` builds the demo into `frontend/dist/demo/`. The Demo page workflow publishes two to the `gh-pages` branch, which GitHub Pages serves: the latest release at https://chriszly.github.io/usage-control/ and main at https://chriszly.github.io/usage-control/main/. A published release replaces only the root and a change to main only `main/`. It passes `--base-href` and, through `--define`, the release's version (`DEMO_RELEASE`) or main's commit (`DEMO_COMMIT`) for the notice above the page, which links to the other one, and for the version the sample devices report (`src/demo/demo-build.ts`). It is the real page, started from `src/demo/main.ts`, which answers the page's requests with made-up devices from `src/demo/fleet.ts` instead of a backend; devices cannot be added or removed there. The normal build does not contain any of it. When a new value is added to the API, give the sample devices that report it a value in `fleet.ts` too, so the demo shows it.

## Tray icon and theme colors

The Windows tray icon is the website's mascot (`frontend/src/app/mascot/mascot.html` and `mascot.css`) in the colors of the website's theme: the `--mascot-*` tones that `frontend/src/styles.scss` takes from its `$palette`. To recolor the website, the bear and the tray icon together, change `$palette` there; the next build follows. The icons are drawn at build time, after the website:

```bash
cd frontend && npm ci && npm run build
cd ../backend && go run ./cmd/tray-icons
GOOS=windows go build -ldflags=-H=windowsgui ./cmd/usage-control-tray
```

`tray-icons` writes `usage-control.ico` (the installer's icon) and `running.ico`, `stopped.ico` and `paused.ico`, the bear with a green, red or grey status dot, into `backend/cmd/usage-control-tray/icons/`. They are not committed. It understands the circles, ellipses and paths (`M`, `L`, `H`, `V`, `A`) the mascot uses and stops with an error on anything else. The menu's words are in `backend/cmd/usage-control-tray/tray.go`, in the same four languages as the website.

## Translations

The website's text lives in `frontend/src/app/i18n/messages/`: `en.ts` (British English) is the source, and `de.ts`, `fr.ts` and `es.ts` must have the same keys, which the TypeScript compiler checks, so the build fails when a translation is missing. `en-us.ts` (American English) takes the British text and replaces only what is written differently in the US, such as the `format.*` date patterns with the 12-hour clock, so a new key needs no American copy. Dates are formatted with these patterns, never with a pattern written into a component. The `I18n` service holds the page's language as a signal, so switching it updates the page in place. The page starts in the language picked last time (kept in the browser's localStorage), else the browser's language (American English for `en-US`, British English for any other English), else British English.

- Add a text to `en.ts` and the same key to the other three files. A `{name}` in a text is a placeholder; every translation must use the placeholders of the English text, which a test checks
- Show it with `i18n.t('area.name')` (or `i18n.t('area.name', { name: value })`) in a template or a `computed`, after `protected readonly i18n = inject(I18n)`
- Format numbers and dates in the page's language by passing it to the pipe: `value | number: '1.0-1' : i18n.language()`, `time | date: format : undefined : i18n.language()`, `bytes | bytes: i18n.language()`

## Database changes

To change a table, append a migration to `migrations` in `backend/internal/history/migrate.go`; never change one a release already has. See [Updates to the layout](database.md#updates-to-the-layout).

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
- Shell scripts: `bash -n` and `shellcheck`

## Releases and builds

| Workflow | Builds |
| --- | --- |
| `ci.yml` | every check above, plus a build-only multi-arch Docker image on pull requests, whose amd64 image is then started with the `power` and `processes` add-on containers from `compose.yaml` until the page shows the processes add-on |
| `docker.yml` | publishes `ghcr.io/chriszly/usage-control:main` on every merge to main, and `X.Y.Z`, `X.Y` and `latest` on release tags |
| `windows.yml` | the x64 and arm64 MSIs, installs and uninstalls them on Windows; attaches them to releases |
| `linux.yml` | the Linux archives, tests install, update and uninstall under systemd; attaches them to releases |
| `vulnerabilities.yml` | every night, the updates that fix known vulnerabilities on main (`ci/fix-vulnerabilities.sh`), as one pull request; fails when a vulnerability has no fix yet |
| `demo.yml` | publishes the demo of a release to the root of GitHub Pages when the release is published, and the demo of main to `main/` on every merge to main that changes the frontend |

A release is published on GitHub with a tag such as `1.0.4`; the workflows build and attach everything on their own.

The version counts the pull requests merged since the last release, in order:

- Each feature raises the last number: `1.0.3` becomes `1.0.4`. The tenth feature raises the middle one instead: `1.0.9` becomes `1.1.0`.
- Each bugfix adds a letter or moves it on: `1.0.4` becomes `1.0.4a`, then `1.0.4b`; after `z` comes `aa`. The next feature drops the letters again.

Docker images and the update check use the version as it is. A Windows installer's version may only hold numbers, so its letters become a fourth number: `1.0.4h` is `1.0.4.8` in Windows' list of apps, and still installs over `1.0.4`.

## Repository

- [AGENTS.md](../AGENTS.md): code style, security, testing and pull request rules
- [.github/PULL_REQUEST_TEMPLATE.md](../.github/PULL_REQUEST_TEMPLATE.md): every pull request fills it in; the "PR Template Validation" workflow checks it
- `ci/`: checks that CI runs and that you can run locally
- [SECURITY.md](../SECURITY.md): how to report a vulnerability and who can change the code
