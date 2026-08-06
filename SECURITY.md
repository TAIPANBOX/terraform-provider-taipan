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

**`main` is not what the Terraform Registry serves.** A fix on `main` protects
nobody until it is cut as a release (see "Publishing a release" in the README) and
the Registry ingests it, and that step is currently manual, not tag-triggered (same
README section). This gap is not hypothetical: GO-2026-6061 was fixed on `main` on
2026-07-28 and did not reach the Registry as `0.1.1` until 2026-08-05, eight days
during which `terraform init` kept resolving the vulnerable `0.1.0`. Before trusting
"it's fixed on main" as an answer, check what the Registry is actually serving:

```sh
curl -s https://registry.terraform.io/v1/providers/TAIPANBOX/taipan/versions | jq -r '.versions[].version'
```

or check the [Registry page](https://registry.terraform.io/providers/TAIPANBOX/taipan) directly. If
the latest listed version predates a fix you need, treat it as unreleased, not fixed.

## Verifying a build

Every change must pass the full gate before merge: `gofmt -l .` clean,
`go vet ./...`, `go build ./...`, and `go test -race ./...`. Run `make
govulncheck` and `make gosec` before a release. See
[CONTRIBUTING.md](CONTRIBUTING.md).
