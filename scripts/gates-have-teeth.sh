#!/usr/bin/env bash
# Checks that the gates in `scripts/` still FAIL on the faults they exist to
# catch, still PASS on what they must not catch, and REFUSE to report success
# when they measured nothing at all.
#
# WHY
#
# Every gate here parses text, and a text parser does not break loudly: it
# stops matching and reports success. The mutants that proved each one existed
# as prose, in commit messages and in the `*(gate: ...)*` markers in CLAUDE.md,
# which is a record of what was true once. Nothing ran them again.
#
# A gate that has quietly stopped catching anything looks exactly like a gate
# with nothing to catch, and stays that way until the fault it guards ships.
#
# WHY THE THIRD PROPERTY IS SEPARATE FROM THE FIRST
#
# `docs-generated.sh` already refuses in three distinct ways when it cannot
# measure: tfplugindocs not installable, the tracked tree not copyable,
# generation itself failing. Every one of those sentences was true, was
# established by hand once in the session that wrote it, and nothing re-ran
# them.
#
# It is also the gate with the most ways to go quiet. It installs a tool,
# copies a tree, regenerates docs and diffs them. Any link in that chain can
# stop producing output without the diff having anything to compare, and a diff
# of nothing against nothing is empty, which reads exactly like agreement.
#
# HOW IT MUTATES WITHOUT LEAVING A MESS
#
# It edits tracked files in place, so it refuses to start unless the tree is
# clean, restores with `git checkout` after every case, restores again from a
# trap on any exit path including a kill, and asserts the tree is clean before
# reporting success.
#
#
# A GATE THAT IS ALREADY FAILING CANNOT BE JUDGED
#
# No case proves anything if the gate was already failing before the mutation.
# So every case runs the gate on the UNMUTATED tree first and reports
# UNJUDGEABLE. Found on 2026-08-09 in it-rat, where one gate was legitimately
# red and a case against it would have been indistinguishable from a working
# one.
#
# It covered only the fail-cases at first, which left the mirror of the same
# bug: on a red gate a pass-case reports OVEREAGER, "the gate failed on
# something it must not catch", and sends the reader to look at a harmless
# mutation. The verdict was being given without the predicate it depends on.
#
# A MUTATION THAT DID NOT APPLY PROVES NOTHING
#
# Every edit asserts it changed the file. A case whose edit applied nothing is
# a failure here, not a pass. That is not hypothetical: five such mutations
# were caught across idryx and tokenfuse on 2026-08-09, and three of the five
# had been verified BY HAND against the same gate minutes earlier. The hand
# version and the harness version differ only in how many layers of quoting sit
# between the text and python, which is exactly the difference nobody sees.

set -uo pipefail
cd "$(git rev-parse --show-toplevel)" || exit 1

if [ -n "$(git status --porcelain)" ]; then
	printf 'this script mutates tracked files, so it needs a clean tree.\n'
	printf 'commit or stash first; it restores with `git checkout` and cannot\n'
	printf 'tell your edits from its own.\n'
	exit 1
fi

# Untracked files too: a mutation may RENAME a tracked file, and `git checkout`
# restores the original while leaving the new name behind. And the INDEX, since
# a gate may read `git ls-files` rather than the disk, so a mutation has to move
# the file in both. Safe because this
# script refuses to start unless the tree is clean, so anything untracked
# during a run was created by the run. `-x` is deliberately absent: ignored
# build output is not ours to delete.
restore() {
	git reset -q --hard HEAD 2>/dev/null
	git clean -fdq 2>/dev/null
}
baseline_dir="$(mktemp -d)"

# One trap for both, because a second `trap ... EXIT` REPLACES the first
# rather than adding to it. Writing them separately disarmed `restore` on
# every interrupt path, which would leave a mutated tree behind on Ctrl-C.
cleanup() {
	restore
	rm -rf "$baseline_dir"
}
trap cleanup EXIT INT TERM


failures=0
cases=0

# run_case <name> <expect: fail|pass> <gate> <python edit> [required output]
#
# The needle separates "it failed" from "it failed for the reason this case is
# about". Without it, a case expecting failure is satisfied by any failure,
# including one this harness caused itself.
run_case() {
	local name="$1" expect="$2" gate="$3" edit="$4" needle="${5:-}"
	cases=$((cases + 1))

	# The baseline applies to EVERY case, not only the ones expecting a failure.
	# It was `fail`-only until 2026-08-09, which left the mirror of the bug it was
	# written for: on a gate that is already red, a `pass` case reports OVEREAGER,
	# "the gate failed on something it must not catch", and sends the reader to
	# look at a harmless mutation while the gate was failing without it. Neither
	# verdict means anything on a red gate, so neither is given.
	skip_baseline=0
	if [ "$expect" = fail_env ]; then
		# `fail` with the baseline skipped, for cases whose fault IS the command
		# rather than a mutation: red before and after is the point there.
		expect=fail
		skip_baseline=1
	fi

	if [ "$skip_baseline" = 0 ]; then
		local key base_out
		key="$baseline_dir/$(printf '%s' "$gate" | cksum | tr -d ' ')"
		if [ ! -f "$key" ]; then
			if eval "$gate" >/dev/null 2>&1; then printf 'green' >"$key"; else printf 'red' >"$key"; fi
		fi
		base_out="$(cat "$key")"
		if [ "$base_out" = red ]; then
			printf 'UNJUDGEABLE  %s\n             the gate is already failing on a clean tree, so neither a\n             failure nor a pass after the mutation would prove anything\n' "$name"
			failures=$((failures + 1))
			return
		fi
	fi

	if ! python3 -c "$edit"; then
		printf 'BROKEN  %s\n        its mutation did not apply, so this case proved nothing\n' "$name"
		failures=$((failures + 1))
		restore
		return
	fi

	local out rc
	out=$(eval "$gate" 2>&1)
	rc=$?
	restore

	# Exit code first, then wording. Checking the needle before the expectation
	# turns "it did not fail at all" into "it failed for the wrong reason",
	# which sends the reader to look at prose when the gate is toothless.
	if [ "$expect" = fail ] && [ "$rc" -ne 0 ] && [ -n "$needle" ] &&
		! printf '%s' "$out" | grep -qF -- "$needle"; then
		printf 'WRONG REASON  %s\n              it failed, but not saying: %s\n' "$name" "$needle"
		failures=$((failures + 1))
		return
	fi
	if [ "$expect" = fail ] && [ "$rc" -eq 0 ]; then
		printf 'TOOTHLESS  %s\n           the gate passed on a fault it exists to catch\n' "$name"
		failures=$((failures + 1))
	elif [ "$expect" = pass ] && [ "$rc" -ne 0 ]; then
		printf 'OVEREAGER  %s\n           the gate failed on something it must not catch\n' "$name"
		failures=$((failures + 1))
		printf '%s\n' "$out" | head -4 | sed 's/^/           /'
	else
		printf 'ok  %-58s (%s)\n' "$name" "$expect"
	fi
}

py() { printf 'def edit(p, a, b):\n    s = open(p).read()\n    assert a in s, "pattern not found in " + p\n    open(p, "w").write(s.replace(a, b, 1))\n%s\n' "$1"; }

echo "=== faults each gate must catch ==="

# invariant 4: this provider goes into other people's Terraform, so a
# dependency here is a dependency for everyone who uses it. The indirect one is
# promoted rather than invented, so go.sum does not move.
run_case "deps-tight: an undeclared direct dependency" fail \
	'./scripts/deps-tight.sh' \
	"$(py 'edit("go.mod", "github.com/agext/levenshtein v1.2.3 // indirect", "github.com/agext/levenshtein v1.2.3")')" \
	"undeclared direct dependency"

# The reverse: the allow-list describing a repo that no longer exists.
run_case "deps-tight: a declared dependency gone from go.mod" fail \
	'./scripts/deps-tight.sh' \
	"$(py 'edit("go.mod", "\tgithub.com/hashicorp/terraform-plugin-log v0.10.0\n", "")')" \
	"is gone from go.mod"

# invariant 6: docs are generated, never hand-edited. A hand edit survives
# until the next regeneration and then vanishes, taking the correction with it.
run_case "docs-generated: a doc page edited by hand" fail \
	'./scripts/docs-generated.sh' \
	"$(py 'edit("docs/resources/budget.md", "# taipan_budget (Resource)", "# taipan_budget (Resource)\n\nA sentence somebody added by hand, which the next regeneration deletes.")')" \
	"the committed docs are not what the schema generates"

# The scenarios exist so what this provider promises can be read without
# reading Go. The drift that breaks that is not a deleted scenario, it is a
# RENAMED test: the rename is ordinary housekeeping, the scenario goes on
# reading like a specification, and nothing runs it any more.
run_case "scenarios-have-tests: a tag naming a test that was renamed" fail \
	'./scripts/scenarios-have-tests.sh' \
	"$(py 'edit("internal/provider/mapping_test.go", "func TestStringOrNullKeepsAnAbsentServerFieldOutOfState(", "func TestStringOrNullKeepsAnAbsentServerFieldOutOfStateRenamed(")')" \
	"names no Go test that exists"

run_case "scenarios-have-tests: a scenario with no tag at all" fail \
	'./scripts/scenarios-have-tests.sh' \
	"$(py 'p = "features/configuring-the-provider.feature"
s = open(p).read()
open(p, "w").write(s + "\n  Scenario: A promise nobody bound to a test\n    Given nothing holds this\n")')" \
	"no @test: tag"

# invariant 10: a number this README states about itself is re-read. Both
# directions of the floor: a claim above what the tests reach, and tests that
# fall through a claim that was true when written.
run_case "readme-numbers: a coverage floor above what the tests reach" fail \
	'./scripts/readme-numbers.sh' \
	"$(py 'edit("README.md", "at least 43% of statements", "at least 95% of statements")')" \
	"and the README claims at least"

run_case "readme-numbers: a scenario count that does not match features/" fail \
	'./scripts/readme-numbers.sh' \
	"$(py 'edit("README.md", "**9 scenarios**", "**14 scenarios**")')" \
	"scenarios and features/"

echo
echo "=== and what they must NOT catch ==="

run_case "deps-tight: another indirect dependency added" pass \
	'./scripts/deps-tight.sh' \
	"$(py 'edit("go.mod", "\tgithub.com/agext/levenshtein v1.2.3 // indirect", "\tgithub.com/agext/levenshtein v1.2.3 // indirect\n\tgithub.com/kr/pretty v0.3.1 // indirect")')"

# Only ONE direction is checked, on purpose. Most tests here are not promises
# to an operator: the sensitive-attribute walkers and the client tests hold
# invariants, not documented behaviour. A gate demanding a scenario per test
# would get scenarios deleted to keep it quiet.
run_case "scenarios-have-tests: a test with no scenario, which is allowed" pass \
	'./scripts/scenarios-have-tests.sh' \
	"$(py 'p = "internal/provider/mapping_test.go"
s = open(p).read()
open(p, "w").write(s + "\nfunc TestAnAssertionThatIsNotAPromise(t *testing.T) { _ = t }\n")')"

# A floor UNDER what the tests reach is understating, not lying. It gets a loud
# line and must not be a red exit: refusing it would teach people to leave the
# number off the README entirely, which is the outcome this gate exists against.
run_case "readme-numbers: a floor well under actual, which is not a failure" pass \
	'./scripts/readme-numbers.sh' \
	"$(py 'edit("README.md", "at least 43% of statements", "at least 20% of statements")')"

echo
echo "=== and the one this estate learned the hard way ==="
echo "    a gate whose subject is gone must SAY so, not report OK on nothing"

# The whole subject of the docs gate removed. A diff of nothing against nothing
# is empty, and empty reads exactly like agreement, so this must fail instead.
# It answers with the disagreement rather than with "measured nothing", which
# is correct here and worth stating: the schema DOES generate pages, and none
# of them is committed, so that is a real mismatch and not an empty read. The
# genuinely-measured-nothing paths in this gate are the ones it names itself,
# tfplugindocs failing to install and the tree failing to copy, and neither can
# be provoked from a mutation to the repository.
run_case "scenarios-have-tests: no feature file left" fail \
	'./scripts/scenarios-have-tests.sh' \
	"$(py 'import subprocess
subprocess.run(["git", "mv", "features", "features-elsewhere"], check=True)')" \
	"measured nothing"

run_case "scenarios-have-tests: no _test.go left anywhere" fail \
	'./scripts/scenarios-have-tests.sh' \
	"$(py 'import subprocess, glob
for f in glob.glob("internal/**/*_test.go", recursive=True):
    subprocess.run(["git", "mv", f, f + ".moved"], check=True)')" \
	"measured nothing"

run_case "readme-numbers: no README left to read" fail \
	'./scripts/readme-numbers.sh' \
	"$(py 'import subprocess
subprocess.run(["git", "mv", "README.md", "README-elsewhere.md"], check=True)')" \
	"measured nothing"

run_case "docs-generated: no committed docs left to compare" fail \
	'./scripts/docs-generated.sh' \
	"$(py 'import subprocess, glob
n = 0
for f in glob.glob("docs/**/*.md", recursive=True):
    subprocess.run(["git", "rm", "-q", f], check=True)
    n += 1
assert n, "no generated docs in this repo"')"

echo
if [ -n "$(git status --porcelain)" ]; then
	printf 'FAIL: this script left the tree dirty, so it cannot be trusted about anything above\n'
	git status --porcelain | head -5
	exit 1
fi

if [ "$failures" -gt 0 ]; then
	printf '%d of %d cases failed.\n' "$failures" "$cases"
	printf 'A gate that has quietly stopped catching anything looks exactly like a gate\n'
	printf 'with nothing to catch, and stays that way until the fault it guards ships.\n'
	exit 1
fi

printf 'OK: %d cases. Every gate fails on its own fault, passes on a non-fault,\n' "$cases"
printf '    and refuses to report success when it measured nothing.\n'
