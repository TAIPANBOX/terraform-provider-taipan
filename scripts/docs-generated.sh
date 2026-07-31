#!/usr/bin/env bash
# Enforces invariant 6 of CLAUDE.md: docs are generated, never hand-edited.
#
# A hand edit to docs/ survives until the next generation and then vanishes,
# usually silently and usually right after somebody relied on it. Worse, the
# Registry serves docs/ verbatim: a schema change without a regeneration
# publishes a description of a provider that no longer exists, to a version that
# can never be edited or withdrawn.
#
# THE INVOCATION MATTERS AND IS NOT THE DEFAULT. `tfplugindocs` derives the
# provider name from the directory, giving "terraform-provider-taipan", while
# the provider is called `taipan` in every Terraform configuration and in every
# committed page title. Running it plain rewrites all three page titles to a
# name that does not exist. The first version of this check did exactly that and
# reported the committed docs as stale; they were right and the invocation was
# wrong. The Makefile's `docs` target had the same bug and would have corrupted
# the titles for anybody who ran it.
#
# IT GENERATES INTO A TEMPORARY DIRECTORY and compares, rather than regenerating
# in place and inspecting `git status`. The in-place version needed a clean tree
# to prove anything, which pushes whoever is testing it toward committing and
# resetting around it. That is how the first draft of this script deleted
# itself.
#
# It does NOT skip when tfplugindocs is missing. `make docs` used to, and a
# target that prints "skipping" and exits zero is how a repository convinces
# itself it is checked.
#
# This file is the ONE copy of this check.

set -uo pipefail
cd "$(git rev-parse --show-toplevel)" || exit 1

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

TOOL=$(command -v tfplugindocs || true)
if [ -z "$TOOL" ]; then
	if ! GOBIN="$WORK/bin" go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest >"$WORK/install.log" 2>&1; then
		echo "FAIL: could not install tfplugindocs, so this check measured nothing."
		echo "      It deliberately does not skip: a check that prints 'skipping'"
		echo "      and exits zero is how a repo convinces itself it is checked."
		tail -3 "$WORK/install.log"
		exit 1
	fi
	TOOL="$WORK/bin/tfplugindocs"
fi

# A copy of the tracked files AS THEY ARE ON DISK, so nothing here can touch the
# working directory whatever it does.
#
# Tracked files as they are, not `git archive HEAD`: the first version exported
# the committed tree, so a schema edit that had not been committed yet was
# invisible and the check said OK. In CI the tree is the commit and both are the
# same; locally they are not, and the version that misleads locally is the wrong
# one to keep. Untracked files stay out either way, so a scratch file cannot
# affect the result.
SRC="$WORK/src"
mkdir -p "$SRC"
if ! git ls-files -z | tar --null -cf - -T - 2>/dev/null | tar -xf - -C "$SRC" 2>/dev/null; then
	echo "FAIL: could not copy the tracked tree, so nothing was compared"
	exit 1
fi

if ! (cd "$SRC" && "$TOOL" generate --provider-name taipan) >"$WORK/gen.log" 2>&1; then
	echo "FAIL: tfplugindocs could not generate the docs"
	tail -5 "$WORK/gen.log"
	exit 1
fi

if ! diff -ru --strip-trailing-cr docs "$SRC/docs" >"$WORK/diff.txt" 2>&1; then
	echo "FAIL: the committed docs are not what the schema generates."
	echo "      The Registry serves docs/ verbatim and a published version can"
	echo "      never be edited, so this would ship a description of a provider"
	echo "      that does not exist."
	echo
	head -30 "$WORK/diff.txt"
	echo
	echo "Run: tfplugindocs generate --provider-name taipan"
	echo "See CLAUDE.md invariant 6."
	exit 1
fi

echo "OK: the committed docs are exactly what the schema generates."
