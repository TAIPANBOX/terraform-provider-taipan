# Validation

Every other repository in this stack carries a `VALIDATION.md`, and this one
did not. This file says what has actually been checked about the provider, and
what has not.

A Terraform provider is a piece of software whose failure mode is quiet: a
resource that reports success while touching nothing looks exactly like a
resource that worked. So the interesting evidence is not the unit tests, it is
what happened when the provider was pointed at real backends.

## Two layers, and only one of them proves much

**33 unit tests** (`go test ./internal/provider -count=1`, green; measured via
`go test ./internal/provider/... -list '.*'`, the same reproducible count the
sibling repos' `readme-numbers.sh`-style gates use, minus the five
`TestAcc*` names) drive each resource against `httptest` mocks that mirror
`tokenfuse-cloud`'s and `wardryx`'s real API shapes: method, path, headers,
body, and the not-found and error branches each Read and Delete depends on.
Useful, and limited: a mock agrees with whatever the code asks it.

**Acceptance tests** (`TestAccBudgetResource`, `TestAccUnitBudgetResource`,
`TestAccWardryxPolicyResource`) drive the real provider over the actual
Terraform protocol v6 wire against a real `tokenfuse-cloud` and a real
`wardryx serve`, covering the plan and state handling inside
Create/Read/Update/Delete that the mocks deliberately cannot reach. `make
testacc` builds and starts both backends from sibling checkouts, waits for
`/healthz`, runs the tests, and tears both down on exit either way. CI runs
that same script unmodified.

Two more (`TestAccAgentPassportFilesystemModels`,
`TestAccAgentPassportResource_Import`) are also `TF_ACC`-gated but need no
live backend at all, since `taipan_agent_passport` calls no API: just a
`terraform`/`tofu` binary on `PATH`. `make testacc` doesn't invoke these
separately; running the full suite under `TF_ACC=1` (as `testacc-local.sh`
does, `go test -run '^TestAcc'`) covers them alongside the backend-requiring
three.

They are gated on `TF_ACC`, Terraform's own opt-in convention, so a plain
`go test ./...` reports them `SKIP` rather than `FAIL`. That is deliberate:
a test that fails because a backend is absent trains people to ignore red.

## What testing against a real backend caught

Two gaps that mocks could not have surfaced, both found only because the tests
were written against real services rather than against assumptions carried over
from the SDKv2 era:

1. **Neither resource has the `id` attribute the test framework silently
   assumes.** `ImportStateVerify` defaults to an `id`, so both resources need
   `ImportStateId` set explicitly, and `taipan_budget` additionally needs
   `ImportStateVerifyIdentifierAttribute: "run_id"` because it has no `id` at
   all. `taipan_unit_budget` shares `taipan_budget`'s exact shape (no `id`,
   only `unit_id`), so its acceptance test applies the same
   `ImportStateVerifyIdentifierAttribute: "unit_id"` from the start.
   `taipan_agent_passport` is the fourth resource and needed a different fix
   entirely, not just the same attribute: it calls no API, so its Read
   cannot re-derive the rest of the resource from an id the way the other
   three's can from a live server. `ImportState` reads and parses the
   passport file at the given path directly, and the import id is that file
   path, not the passport's own `agent://` id.
2. **Wardryx's `updated_at` has second-level granularity** (`time.RFC3339`, no
   fractional seconds). A Create immediately followed by an Update in the same
   test process can land inside the same wall-clock second, so asserting that
   `updated_at` changed needs a short wait first. Without it the test is a coin
   flip, and a flaky test in CI is worse than a missing one.

**`CheckDestroy` asserts what each resource's own `Delete` actually does**, not
a generic "is it gone". `taipan_wardryx_policy` asserts a real 404, because its
Delete calls a real `DELETE`. `taipan_budget` asserts the budget **survives**,
because its Delete is state-only. Which brings up the honest part.

## The honest scope

- **`taipan_budget`'s Delete does not delete anything.** The API has no delete
  for a budget, so `terraform destroy` removes it from state and leaves the
  budget where it is. The provider says so rather than pretending, and the test
  asserts the real behaviour rather than the expected one. Anyone reading a
  `terraform destroy` as "the budget is gone" would be wrong, and that is worth
  more prominence than a passing test.
- **Drift detection is real, and it is one-directional.** An edit made out of
  band shows up on the next `plan`. The provider notices; it does not prevent.
- **The passport is metadata, not a token.** Nothing here authenticates an
  agent.

## Distribution

Published on the public Terraform Registry as
[`TAIPANBOX/taipan`](https://registry.terraform.io/providers/TAIPANBOX/taipan)
with GPG-signed releases, so a normal `required_providers` block and
`terraform init` pull it, signature-verified, with no build from source. That
is a validation claim of its own: the Registry rejects a release whose
signature does not verify against the published key.
