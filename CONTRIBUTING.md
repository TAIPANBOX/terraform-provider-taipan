# Contributing to terraform-provider-taipan

## Development

```sh
go build ./...        # build
go test -race ./...   # run tests
gofmt -l .             # format check, should print nothing
go vet ./...           # vet
```

Before every commit, this must be clean:

```sh
test -z "$(gofmt -l .)" || (gofmt -l .; exit 1)
go vet ./...
go test -race ./...
go build ./...
```

The Makefile also has `govulncheck` and `gosec` targets - run these before a
release, since this provider reconciles real spend budgets against TokenFuse
Cloud.

## Conventions

- Conventional Commits: `feat:`, `fix:`, `refactor:`, `chore:`, `docs:`, `test:`.
- One logical change per commit.
- `go vet`, `gofmt`, and `go test -race` must pass before a PR.
- A resource's `Read` must reconcile drift accurately: Terraform correctness
  depends on the provider reporting the real remote state, not a cached
  guess.

## Security

See [SECURITY.md](SECURITY.md) for how to report vulnerabilities privately.
