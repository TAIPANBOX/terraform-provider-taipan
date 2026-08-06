#!/usr/bin/env bash
# Enforces invariant 4 of CLAUDE.md: the provider keeps exactly five direct
# dependencies.
#
# A Terraform provider is a supply-chain dependency for everyone who declares
# it, and a published Registry version can never be edited or withdrawn. The
# list is short on purpose and growing it is a decision for the user, not a
# convenience.
#
# Checks the DIRECT require block only. Indirect dependencies are pulled by
# those two and are not ours to choose; pinning them here would just mean the
# check goes stale on the next `go mod tidy`.
#
# What this does NOT check: that agent-stack-go's pin is CURRENT. On
# 2026-08-06 the pin sat at v0.1.0 while that module was tagged through
# v0.5.1 (five releases behind), which is exactly what invariant 5 forbids
# in spirit -- passport_resource.go had hand-rolled local copies of two
# types agent-stack-go/passport had carried since v0.3.0. Decided not to
# gate currency here: unlike presence (this repo's own go.mod, checked
# entirely offline), currency needs to know what the LATEST tag over there
# is, which means either a network call in a gate this repo otherwise runs
# fully offline and deterministically, or checking out the sibling repo the
# way agent-stack-go's own schemas-in-sync.sh does. Either turns this gate
# red on a schedule this repo does not control -- a new agent-stack-go tag,
# on its own, with no commit here -- which is a different failure shape from
# every other gate in this file, all of which fail only on a change made IN
# this repo. Left as an explicit gap rather than a silent one: re-check the
# pin against https://github.com/TAIPANBOX/agent-stack-go/tags by hand
# periodically, and see CLAUDE.md's "Decisions that have no gate yet" for
# the same note.
#
# This file is the ONE copy of this check. The local hook and CI both call it.
# Two copies of one check always diverge, so do not inline it anywhere.

set -euo pipefail

cd "$(dirname "$0")/.."

ALLOWED=(
	"github.com/TAIPANBOX/agent-stack-go"
	"github.com/hashicorp/terraform-plugin-framework"
	"github.com/hashicorp/terraform-plugin-go"
	"github.com/hashicorp/terraform-plugin-log"
	"github.com/hashicorp/terraform-plugin-testing"
)

# go mod edit -json gives the parsed module graph, so we do not hand-parse
# go.mod and get the `// indirect` comment wrong.
direct="$(go mod edit -json | python3 -c '
import json, sys
mod = json.load(sys.stdin)
for r in mod.get("Require") or []:
    if not r.get("Indirect"):
        print(r["Path"])
')"

fail=0

while IFS= read -r dep; do
	[ -n "$dep" ] || continue
	ok=0
	for a in "${ALLOWED[@]}"; do
		[ "$dep" = "$a" ] && ok=1 && break
	done
	if [ "$ok" -eq 0 ]; then
		echo "FAIL: undeclared direct dependency '$dep'"
		fail=1
	fi
done <<<"$direct"

# The reverse direction matters too: if a declared dependency disappears, the
# allow-list is describing a repo that no longer exists.
for a in "${ALLOWED[@]}"; do
	if ! grep -qx "$a" <<<"$direct"; then
		echo "FAIL: expected direct dependency '$a' is gone from go.mod"
		echo "      Either it was removed on purpose, in which case update this"
		echo "      script and CLAUDE.md invariant 4, or it was removed by accident."
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	echo
	echo "This provider runs inside other people's terraform apply, and a"
	echo "published version is permanent. See CLAUDE.md invariant 4."
	echo "Adding a dependency needs the user, not a commit."
	exit 1
fi

echo "OK: direct dependencies are exactly the five declared ones."
