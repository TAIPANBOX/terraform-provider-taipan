# CLAUDE.md, working instructions for terraform-provider-taipan

These instructions apply to any model working in this repo. Read this file
before writing code. It holds process and invariants only: **no status.**
Status goes stale, and a stale instruction file is worse than none. For where
things stand, read the git tags, `VALIDATION.md`, and the README.

## Read before you change anything

1. `README.md`, and the generated docs under `docs/`. The docs are the
   contract a Registry user reads before they ever see the code.
2. `scripts/testacc-local.sh`. It is how the acceptance tests get real
   backends, and acceptance tests are the only thing here that proves anything.
3. `SPEC.md` in the sibling repo `TAIPANBOX/agent-passport`, for the passport
   shape this provider writes.

## What this is

The governance-as-code Terraform provider for the agent-governance stack. It
manages TokenFuse budgets, Wardryx policies and Agent Passports as declarative
resources, applied against live backends through the normal plan and apply
loop. It is published on the Terraform Registry.

## Blast radius, and why this repo is stricter than the others

Two things make a mistake here more expensive than in a sibling repo.

**A published Registry version is permanent.** It cannot be edited or replaced,
only superseded. A broken schema that reaches the Registry is public forever
and someone's `terraform init` will pin to it.

**The provider runs inside other people's `terraform apply`.** A wrong diff
does not print a warning, it changes their infrastructure.

## The working loop

1. Branch off `main`, one logical increment per branch.
2. Run every gate below. All must pass locally before the push.
3. Regenerate docs if you touched a schema, and commit the regenerated files in
   the same commit. A schema change with stale docs is a lie on the Registry.
4. Commit with Conventional Commits. End the message with the standard
   co-author trailer naming the model that actually did the work.
5. Push the branch, open a PR with `gh`.
6. Wait for all CI checks to go green, including acceptance tests.
7. **Ask the user before merging, and again before tagging.** Do not self-merge
   and never tag on your own: see blast radius.

## Gates

```sh
test -z "$(gofmt -l .)"
go vet ./...
staticcheck ./...
go test -race ./...
go build ./...
./scripts/deps-tight.sh
./scripts/docs-generated.sh
./scripts/gates-have-teeth.sh   # invariant 8; needs a clean tree
```

CI additionally runs `govulncheck ./...` and the acceptance suite
(`./scripts/testacc-local.sh`) against a live TokenFuse Cloud and Wardryx.

**A green unit-test run means very little in a Terraform provider.** Unit tests
here check schema shape and mapping; they cannot see a perpetual diff, a
missing `RequiresReplace`, or an import that does not round-trip. Only the
acceptance tests can. Do not report a resource change as verified on the
strength of `go test` alone.

## Hard invariants

Each one carries how it is held today. Use `(gate: ...)`, `(test: ...)`,
`(partly gated: ...)` or `(not enforced)`, and use the weakest one that is
true. An invariant with no check, written as though it had one, is worse than
an absent invariant.

1. **A resource that has not changed must produce an empty plan.** A perpetual
   diff is the classic provider bug, it is invisible to unit tests, and it
   teaches operators to ignore plans. Every resource needs an acceptance step
   that applies, then plans again, and asserts the plan is empty.
   *(partly gated: acceptance tests, where the step was written)*
2. **State reflects the backend, it never invents.** If the API does not return
   a field, the provider does not synthesize one to make the plan quiet. A
   quiet plan built on a guess is worse than a noisy honest one.
   *(not enforced)*
3. **No credential ever reaches state, a log line, or an error message.**
   Terraform state is frequently stored unencrypted and shared. Anything
   sensitive must be marked sensitive in the schema, and error text must not
   echo the value it failed on.
   *(partly gated: `TestEverySecretShapedResourceAttributeIsSensitive`,
   `TestEverySecretShapedProviderAttributeIsSensitive`,
   `TestDataSourcesDeclareNothingUnmarked`. They hold the schema half. The
   error-text half is unchecked, see below.)*
4. **Dependencies stay at the declared five**: `agent-stack-go` plus the four
   `hashicorp/terraform-plugin-*` modules. A provider is a supply-chain
   dependency for everyone who uses it. *(gate: `scripts/deps-tight.sh`)*
5. **`agent-stack-go` is the only source of the wire types**, pinned by tag.
   Never hand-roll a local passport or event type. *(gate:
   `scripts/deps-tight.sh` for its presence, not for its use)*
6. **Docs are generated, never hand-edited.** A hand edit survives until the
   next generation and then vanishes, usually silently and usually right after
   somebody relied on it. **The invocation is `tfplugindocs generate
   --provider-name taipan`, and the flag is not optional:** the default derives
   `terraform-provider-taipan` from the directory, and the provider is called
   `taipan` everywhere a user sees it.
   *(gate: `scripts/docs-generated.sh`)*
7. **A published version is never reused.** Not re-tagged, not force-pushed,
   not deleted from the Registry. Ship a new patch instead. *(not enforced)*

8. **A check must be able to tell "did not fail" from "did not run", and both
   gates here have been made to fail on purpose to prove they can.**
   `docs-generated.sh` already refuses in three distinct ways when it cannot
   measure: tfplugindocs not installable, the tracked tree not copyable,
   generation itself failing. Every one of those sentences was true, was
   established by hand once in the session that wrote it, and nothing re-ran
   them.

   It is also the gate here with the most ways to go quiet, and that is why it
   earns this rather than the dependency check. It installs a tool, copies a
   tree, regenerates docs and diffs them. Any link in that chain can stop
   producing output without the diff having anything to compare, and a diff of
   nothing against nothing is empty, which reads exactly like agreement.
   *(gate: `scripts/gates-have-teeth.sh`, 5 cases: three real faults, one
   non-fault, and one where the whole subject of the docs gate is removed. That
   last one is answered with the disagreement rather than with "measured
   nothing", which is correct: the schema does generate pages and none is
   committed, so it is a real mismatch. The two genuinely-measured-nothing
   paths, tfplugindocs failing to install and the tree failing to copy, cannot
   be provoked by mutating the repository and stay unexercised, which is
   recorded here rather than glossed.)*

   **What it does not cover.** It cannot test itself. It proves each gate
   catches the faults named in it, not every fault of that kind. It found no
   hole in either.

## Decisions that have no gate yet

This list is debt, and it is here to stay visible rather than to be tidy.

**Held by this file alone: invariants 2, 6 and 7.** Invariant 3 is half held.

Invariant 3's schema half is now three tests. They walk the REAL schema objects
returned by each `Schema()` method rather than the source text, so they see what
the framework will actually use, including nested attributes, and cannot be
fooled by a differently formatted declaration. Anything whose name says key,
secret, token, password or credential must be `Sensitive`. Verified by breaking:
an unmarked `legacy_api_token` on the provider and an unmarked `override_secret`
on a resource are both caught, and the same attribute WITH the marker passes.

Two limits, stated rather than implied. The check is **name-driven**, because
nothing can know which attributes carry secrets; an attribute holding a secret
under a name that says nothing is beyond it and stays a matter for review. And
the **error-text half is unchecked**: nothing stops a diagnostic interpolating
the value it failed on. That one probably needs a reviewer, since a format
string can carry a secret through any number of hops.

The `notActuallySecret` allow-list exists so that exempting a name is a decision
with a written reason rather than a shortcut.

Invariant 6 is now `scripts/docs-generated.sh`, and it was not the two-line
check this paragraph imagined. Two things had to be got right.

**The invocation is not the default.** Running `tfplugindocs generate` plain
rewrites all three page titles from `taipan` to `terraform-provider-taipan`. The
first version of the check did that and reported the committed docs as stale;
they were correct and the invocation was wrong. `make docs` had the same bug and
would have corrupted the titles for anybody who ran it, so the Makefile is fixed
too, and it no longer prints "skipping" and exits zero when the tool is absent.

**It generates into a temporary copy and diffs**, rather than regenerating in
place and reading `git status`. The in-place version needed a clean tree to
prove anything, which pushes whoever is testing it toward committing and
resetting around it; that is how the first draft deleted itself. The copy is of
tracked files as they are ON DISK, not of `HEAD`, so an uncommitted schema edit
is caught: in CI the two are the same, and locally the version that stays quiet
is the wrong one to keep. That is strictly better than asking people to remember.

Invariant 1 is gated only where somebody wrote the empty-plan step. A helper
that every resource's acceptance test must call would make it uniform.

**Invariant 5's "the only source" half had no teeth until 2026-08-06, and the
gap was real, not hypothetical.** The pin sat at `v0.1.0` while
`agent-stack-go` was tagged through `v0.5.1`, and `passport_resource.go` had
hand-rolled `passportFilesystemDoc`/`passportModelDoc`, field-for-field
copies of `passport.FsScope`/`passport.Model`, which agent-stack-go had
carried since `v0.3.0`. Fixed by bumping the pin and deleting the local
copies in favor of the upstream types.

`scripts/deps-tight.sh` still only checks presence, not currency, and that
stays a deliberate gap rather than a new gate: checking currency needs to
ask what `agent-stack-go`'s latest tag IS, which this repo cannot answer
from its own tree the way presence can. Either a network call or a sibling
checkout (agent-stack-go's own `schemas-in-sync.sh` does the latter) would
make this gate fail when a new tag ships over there, with no commit made
here at all, a different failure shape from every other gate in this file.
Left as a debt note instead: re-check
`https://github.com/TAIPANBOX/agent-stack-go/tags` by hand periodically. See
`scripts/deps-tight.sh`'s own comment for the same reasoning in place.

## Standing rule

An approved architecture decision is **not finished** until it is two things: a
numbered invariant in this file, and a gate in a script or a test if it can be
checked structurally. Until then it is a document, and documents do not stop
code.

## Escalate, do not push through

Stop and tell the user, then wait, when a task hits any of these:

- **Any tag or Registry publish, always.** It is irreversible.
- Any schema change, including adding an attribute, because it lands in
  generated docs and in other people's state.
- Anything touching `RequiresReplace`, since getting it wrong destroys real
  resources on somebody else's apply.
- Adding a dependency, or bumping the `agent-stack-go` tag.

Routine work: tests, doc comments, error-message wording that reveals nothing
sensitive, refactors that leave the schema byte-identical.

## Conventions

- **No long dashes** anywhere: not in code comments, docs, commit messages, or
  PR bodies. Use a comma, a colon, parentheses, or a short hyphen.
- Nothing paid or metered gets enabled without telling the user first and
  getting agreement. The acceptance tests talk to live backends; know what they
  cost before running them against anything but a disposable local stack.
- Do not delete or revoke keys, tokens, or certificates on your own initiative.
