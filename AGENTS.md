# AGENTS.md - Instructions for AI Agents

This file provides guidance for AI agents (Claude, GPT, Copilot, etc.) working on the usage-control project.

## Project Overview

usage-control is a website that monitors the hardware it runs on: it reads the usage of the machine (CPU, memory, disk, temperature and so on) and shows it in the browser. The tech stack is not decided yet; this file names it once it is.

## Code Style & Conventions

- Match the surrounding code: its naming, comment density and idioms
- Keep changes small and focused; one topic per pull request
- Shell scripts (`ci/*.sh`) use `#!/usr/bin/env bash` and `set -euo pipefail`, and pass `bash -n` and `shellcheck`
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
- Run the project's tests and linters once the stack adds them, and add tests for new code
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
