# Security Policy

terraform-provider-taipan applies changes to real TokenFuse Cloud spend
budgets and Agent Passports, so a bug here can misconfigure real enforcement
controls. This document covers how to report a vulnerability.

## Reporting a vulnerability

Please report security issues privately, not in public issues or PRs:

- Open a **GitHub private security advisory**:
  <https://github.com/TAIPANBOX/terraform-provider-taipan/security/advisories/new>

Include the affected version/commit, a description, and a minimal reproduction.
We aim to acknowledge within a few days and to fix high-severity issues before
any public disclosure. There is no bug-bounty program; we credit reporters in
the advisory unless you prefer otherwise.

## Supported versions

terraform-provider-taipan is pre-1.0; only `main` is supported. Fixes land on
`main` and are not backported.

## Verifying a build

Every change must pass the full gate before merge: `gofmt -l .` clean,
`go vet ./...`, `go build ./...`, and `go test -race ./...`. Run `make
govulncheck` and `make gosec` before a release. See
[CONTRIBUTING.md](CONTRIBUTING.md).
