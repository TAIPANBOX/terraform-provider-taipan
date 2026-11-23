# Changelog

## Unreleased, since v0.2.0

Twenty-six commits on `main` after the v0.2.0 tag (2026-09-13). Nothing here is
tagged or published yet: the Terraform Registry still serves v0.2.0, and a tag
waits for the owner (CLAUDE.md, the working loop). No resource schema changed,
so a configuration written for v0.2.0 plans the same way.

### Fixed

- **`taipan_agent_passport.attestation_method` names all six methods.** The
  description, and the Registry page generated from it, listed five while
  agent-passport's closed enum has six; `dpop-key` was missing (#53). The value
  is still not validated by the provider.
- **`components.json` declares the four environment fallbacks.** `provider.go`
  reads `TOKENFUSE_CLOUD_URL`, `TOKENFUSE_CLOUD_KEY`, `WARDRYX_URL` and
  `WARDRYX_KEY` behind the provider block, while the manifest claimed it read no
  environment; the manifest test now reads every environment read (#55).

### Security

- **Go toolchain 1.27.4**, by way of 1.27.2 (#50), 1.27.3 for GO-2026-6809 in
  `crypto/x509` (#60) and 1.27.4 for GO-2026-6859 (#65).
- **`google.golang.org/grpc` v1.83.2** (indirect): govulncheck reported
  GO-2026-6348 and GO-2026-6061/6443 as reachable on v1.82.1, and reports none
  after the move (#44).
- **agent-stack-go v2.0.3**, through v1.0.1 (an RSA modulus bounded in
  delegation's key parsing, a path this provider does not reach), v1.0.2,
  v1.1.0, v1.3.0, the v2 module (#58) and the Go rebuilds v2.0.1 to v2.0.3
  (#44, #45, #47, #56, #58, #59, #62, #66). Nothing on the provider's wire moved.
- **The estate secret scan** runs on every pull request and push to `main` (#51).

### Documentation and gates

- README pins `~> 0.2`, lists the six attestation methods and says which
  resources take an import id (#54), and lists `taipan_unit_budget` with the
  other TokenFuse Cloud resource (#49).
- Publishing is the tag path, recorded as proven on v0.2.0 (#42).
- No gate pipes into a reader that leaves early (invariant 12, #63).
- GitHub Actions and Dependabot bumps (#43, #46, #48, #52, #61, #64).
