# usage-control

A website that shows the usage of the hardware it runs on (CPU, memory, disk, temperature and so on).

It is built with a Go backend and an Angular frontend (storing history in SQLite comes next), and runs first on Linux (starting with a Raspberry Pi) with Docker. A native Windows installer comes later. See [AGENTS.md](AGENTS.md#tech-stack) for the details.

It shows CPU and memory usage, temperature sensors and uptime so far, refreshed every two seconds, and is only reachable from the local network.

## Run it with Docker (Linux, Raspberry Pi)

```bash
cp .env.example .env   # optional, to change the port
docker compose up -d --build
```

Then open `http://<the machine's address>:8080` from a device on the same network. The container reads the host's `/proc` and `/sys` read-only, runs as a non-root user and has no extra privileges.

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
(cd backend && gofmt -l . && golangci-lint run && go test ./...)
(cd frontend && npm run format:check && npm run lint && npm test && npm run build)
```
