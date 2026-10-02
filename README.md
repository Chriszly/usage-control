# usage-control

A website that shows the usage of the hardware it runs on (CPU, memory, disk, temperature and so on).

The website is not written yet. This repository so far holds the rules for contributors and the CI:

- [AGENTS.md](AGENTS.md): code style, security, testing and pull request rules (also read by AI agents through `CLAUDE.md`)
- [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md): every pull request fills it in; the "PR Template Validation" workflow checks it
- `ci/`: checks that CI runs and that you can run locally

## Checks

Run before opening a pull request:

```bash
bash ci/check-no-secrets.sh
```
