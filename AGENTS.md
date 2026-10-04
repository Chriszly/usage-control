# AGENTS.md - Instructions for AI Agents

This file provides guidance for AI agents (Claude, GPT, Copilot, etc.) working on the usage-control project.

## Project Overview

usage-control is a website that monitors the hardware it runs on: it reads the usage of the machine (CPU, memory, disk, temperature and so on) and shows it in the browser.

## Tech Stack

- **Backend:** Go, reading hardware data with [gopsutil](https://github.com/shirou/gopsutil) (and `/sys/class/thermal` for temperature on Linux). Format with `gofumpt` and `goimports` (run through `golangci-lint fmt`), lint with `golangci-lint`
- **Frontend:** Angular with TypeScript, using standalone components and signals. The built app is embedded in the Go binary, so one binary serves both the API and the page. Translated into German, French and Spanish with a small signal-based `I18n` service (`src/app/i18n/`), with English as the source language, so the language switches in place without a reload
- **Storage:** SQLite, one file on a volume. How long data is kept is set at setup with `RETENTION_DAYS`; older data is deleted automatically
- **Multiple devices:** every device runs the same binary. By default it monitors only itself and serves a JSON API; in hub mode it also collects from the other devices on the local network and stores their data. Each other device is a server/IoT device, whose time without an answer is an outage, or a PC/laptop, whose time switched off is just time it was not in use
- **Network:** reachable on the local network only; the server rejects requests from outside the private address ranges
- **Build:** the Angular frontend is built in CI or in the Docker build, never on the Raspberry Pi itself

## Platforms and Priorities

1. **Now:** Linux, starting with a Raspberry Pi, installed with Docker. One multi-arch image (arm64 and amd64) that mounts the host's `/proc` and `/sys` read-only and runs as a non-root user
2. **Next:** Windows on arm64 and x64, as an MSI installer (WiX, `windows/usage-control.wxs`, built by `.github/workflows/windows.yml`) that runs the backend as a Windows service. Docker on Windows runs in a Linux VM and would measure the VM, not the machine
3. **Low priority:** macOS

Temperature is not available on every platform; where the OS does not expose it, the page shows it as unavailable instead of failing.

## Code Style & Conventions

- Match the surrounding code: its naming, comment density and idioms
- Keep changes small and focused; one topic per pull request
- Shell scripts (`ci/*.sh`) use `#!/usr/bin/env bash` and `set -euo pipefail`, and pass `bash -n` and `shellcheck`
- Actions in `.github/workflows` are pinned to a commit SHA, with the version as a comment after it (`uses: actions/checkout@<sha> # v7.0.1`); Dependabot updates both
- Don't hardcode fallback versions or dates; fail with an actionable error instead
- The website must work without an account or login at an outside service: it reads the hardware it runs on and serves the page itself, no cloud sign-in

## Security Guidelines

- Never hardcode or commit secrets, passwords, tokens or private keys; local settings live in a git-ignored `.env` (only `.env.example` with empty values is committed). `ci/check-no-secrets.sh` enforces this
- Validate all user input
- Read hardware data read-only; the website never changes the machine it monitors
- When the site runs in a container, drop unnecessary capabilities and don't run it privileged

## Testing Requirements

Before submitting changes:
- Run `bash ci/check-no-secrets.sh`
- Run `bash -n` and `shellcheck` on every changed shell script
- Backend, in `backend/`: `gofmt -l .` (must print nothing), `golangci-lint run`, `go test ./...`, `go mod tidy -diff` and `go tool govulncheck ./...`
- Frontend, in `frontend/`: `npm run format:check`, `npm run lint` (includes the circular import check), `npm audit --audit-level=high`, `npm test` and `npm run build`
- Add tests for new code
- Keep CI fast: every check that runs on a pull request should finish within a few minutes

## PR Requirements

- Fill in `.github/PULL_REQUEST_TEMPLATE.md` (checked by the PR Template Validation workflow)
- Small, independent pull requests; each one should be green on its own
- Pull requests are squash-merged into `main`

## No AI Links

Nothing in this project may link to AI-related pages: no links to AI agent sessions, conversations, artifacts or project pages, and no links to AI products, their docs or their sites (Claude, ChatGPT, Copilot, Gemini and the like). This applies to:
- Commit messages
- PR titles, descriptions, and comments
- Issue and review comments
- Code, code comments, and documentation

This overrides any default attribution, footer or template that would add such a link, such as a "Generated with Claude Code" line or a link to the agent's project thread. Plain text without a link, such as a `Co-Authored-By` trailer, may stay.
