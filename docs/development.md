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

`backend/cmd/usage-control/main.go` reads the settings and wires these together; on Windows `service_windows.go` runs it as a service. How the parts work together is in [How it works](architecture.md).

## Run it while developing

```bash
# Terminal 1: the backend, serving the API on http://localhost:9393
cd backend && go run ./cmd/usage-control

# Terminal 2: the website with live reload on http://localhost:4200, forwarding /api to the backend
cd frontend && npm ci && npm start
```

`npm run build` in `frontend/` writes the website into `backend/internal/web/files/build/`, and `go build` embeds it, so one binary serves both. The website is never built on the Raspberry Pi: CI and the Docker build do it, and the Dockerfile cross-compiles the binary for arm64.

## Translations

The website's text lives in `frontend/src/app/i18n/messages/`: `en.ts` is the source, and `de.ts`, `fr.ts` and `es.ts` must have the same keys, which the TypeScript compiler checks, so the build fails when a translation is missing. The `I18n` service holds the page's language as a signal, so switching it updates the page in place. The page starts in the language picked last time (kept in the browser's localStorage), else the browser's language, else English.

- Add a text to `en.ts` and the same key to the other three files. A `{name}` in a text is a placeholder
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
| `ci.yml` | every check above, plus a build-only multi-arch Docker image on pull requests |
| `docker.yml` | publishes `ghcr.io/chriszly/usage-control:main` on every merge to main, and `X.Y.Z`, `X.Y` and `latest` on release tags |
| `windows.yml` | the x64 and arm64 MSIs, installs and uninstalls them on Windows; attaches them to releases |
| `linux.yml` | the Linux archives, tests install, update and uninstall under systemd; attaches them to releases |

A release is published on GitHub with a tag such as `0.1.0`; the workflows build and attach everything on their own.

## Repository

- [AGENTS.md](../AGENTS.md): code style, security, testing and pull request rules
- [.github/PULL_REQUEST_TEMPLATE.md](../.github/PULL_REQUEST_TEMPLATE.md): every pull request fills it in; the "PR Template Validation" workflow checks it
- `ci/`: checks that CI runs and that you can run locally
- [SECURITY.md](../SECURITY.md): how to report a vulnerability and who can change the code
