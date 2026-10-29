# Contributing

## Before opening a pull request

Run these checks (setup in [docs/development.md](docs/development.md)):

```bash
make check
```

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/).

## Cross-repo contracts

Some changes touch wire contracts shared with other repositories and need coordinated changes in every affected repo:

- `avr/1` formats: the Receipt, the attestation response, the Receipt Evidence Bundle and Raw Capture (protocol, verifier).
- `veriprov/1` Provisioning protocol and Runtime Config schema (protocol, veriprov).
- The Guest Manifest schema (protocol, veriforge, release).
- The Bootstrap and Config Disk (protocol, veriprov, veriforge, verifier).
- The Build Report schema (veriforge, release).
- The `verirelay-component-build/1` component build report written by the release workflow, shared with veriforge and veriprov.

Open the companion pull requests together and reference them from each other.

## Reporting security issues

Vulnerabilities go through private reporting. See [SECURITY.md](SECURITY.md).
