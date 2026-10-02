# usage-control

A website that shows the usage of the hardware it runs on (CPU, memory, disk, temperature and so on).

It will be built with a Go backend, an Angular frontend and SQLite, and run first on Linux (starting with a Raspberry Pi) with Docker. A native Windows installer comes later. See [AGENTS.md](AGENTS.md#tech-stack) for the details.

The website is not written yet. This repository so far holds the rules for contributors and the CI:

- [AGENTS.md](AGENTS.md): code style, security, testing and pull request rules (also read by AI agents through `CLAUDE.md`)
- [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md): every pull request fills it in; the "PR Template Validation" workflow checks it
- `ci/`: checks that CI runs and that you can run locally
- `scripts/deploy.sh` and `compose.yaml`: deploy to a Raspberry Pi (below)

## Deploying to a Raspberry Pi

`scripts/deploy.sh` copies `compose.yaml` and your `.env` to the Pi over SSH,
pulls the image and starts the container with Docker Compose. The Pi needs
Docker with the compose plugin, key login over SSH, and the SSH user in the
`docker` group (rpi-setup's `docker` task sets these up). The image is built
elsewhere for arm64 and pushed to a registry; nothing is built on the Pi.

```bash
cp .env.example .env    # fill in PI_HOST and IMAGE; .env is git-ignored
bash scripts/deploy.sh --dry-run   # show what it would run
bash scripts/deploy.sh
```

A variable on the command line wins over `.env`, e.g.
`PI_HOST=pi4.local bash scripts/deploy.sh`. Running it again pulls the
newest image and recreates the container only when it changed.

## Checks

Run before opening a pull request:

```bash
bash ci/check-no-secrets.sh
```
