#!/usr/bin/env bash
# Every number this README states about its own testing, checked against the
# repository.
#
# WHY A NUMBER NEEDS A GATE
#
# A figure on a README is a claim with no owner. It is right the day it is
# written and nothing tells anybody when it stops being right, because the
# suite grows in commits that never open the README. That is not hypothetical
# in this estate: on 2026-08-04 four of seven figures on it-rat.com were stale,
# and on 2026-08-20 five of eleven were, ten days after the previous sweep.
#
# WHY COVERAGE IS A FLOOR AND NOT AN EXACT FIGURE
#
# An exact percentage would fail on the commit that ADDS a test, which teaches
# people to edit the number rather than read it, and eventually to delete the
# check. A floor is a claim that stays true as coverage rises and goes red the
# moment it falls through, which is the direction worth catching.
#
# The floor being far below actual is not a failure, it is a stale claim
# understating itself. That gets a loud line, not a red exit, on the same
# principle: refusing it would only teach people to leave the number off.
#
# WHAT COVERAGE MEANS HERE, because a number needs a definition more than a badge
#
# `go test -coverprofile` counts STATEMENTS executed by `go test`, which in this
# repository means the unit tests only: the acceptance suite is TF_ACC-gated and
# does not run. So the figure is "statements reached without a live backend",
# and every CRUD method sits outside it by construction.
#
# That is the honest shape and it is worth saying twice, because CLAUDE.md says
# it once already: a green unit run means very little in a Terraform provider.
# This number is a floor under the mapping and plumbing layers. It is not
# evidence that any resource works.

set -euo pipefail

cd "$(dirname "$0")/.."

profile="$(mktemp)"
trap 'rm -f "$profile"' EXIT

go test -coverprofile="$profile" ./... >/dev/null 2>&1 || true

python3 - "$profile" <<'PY'
import pathlib
import re
import subprocess
import sys

profile = sys.argv[1]
readme = pathlib.Path("README.md")
problems = 0


def note(msg):
    global problems
    print(msg)
    problems += 1


if not readme.is_file():
    print("FAIL: README.md is not here, so this measured nothing.")
    sys.exit(1)
text = readme.read_text()

# --- coverage ---------------------------------------------------------------
out = subprocess.run(
    ["go", "tool", "cover", f"-func={profile}"], capture_output=True, text=True
).stdout
m = re.search(r"^total:\s+\(statements\)\s+([0-9.]+)%", out, re.M)
if not m:
    print("FAIL: could not read a total from the coverage profile, so this")
    print("      measured nothing. A missing number is not a passing one.")
    sys.exit(1)
actual = float(m.group(1))

# Tolerant about where the bold starts, strict about the words. The first
# version demanded `at least **N% of statements**` and the README says
# `**at least N% of statements**`, so it refused a correct README. Caught by
# running it, which is the only way this kind of thing is ever caught.
claimed = re.search(r"at least (?:\*\*)?([0-9]+)% of statements", text)
if not claimed:
    note("FAIL: README states no coverage floor, and this check exists to hold one.")
else:
    floor = float(claimed.group(1))
    if actual < floor:
        note(
            f"FAIL: coverage is {actual:.1f}% and the README claims at least "
            f"{floor:.0f}%. Either the claim moves down with a reason, or the "
            f"tests come back."
        )
    elif actual - floor >= 5:
        # Loud, not fatal: understating is not lying, but it stops being useful.
        print(
            f"note: coverage is {actual:.1f}% against a floor of {floor:.0f}%. "
            f"Worth raising the claim; a floor five points low says less every month."
        )

# --- scenarios --------------------------------------------------------------
features = sorted(pathlib.Path("features").glob("*.feature"))
if not features:
    note("FAIL: no .feature file, so the scenario count measured nothing.")
else:
    scenarios = sum(
        len(re.findall(r"^\s*Scenario:", f.read_text(), re.M)) for f in features
    )
    if scenarios == 0:
        note("FAIL: the feature files declare no scenarios at all.")
    claimed_s = re.search(r"\*\*([0-9]+) scenarios\*\*", text)
    if not claimed_s:
        note("FAIL: README states no scenario count.")
    elif int(claimed_s.group(1)) != scenarios:
        note(
            f"FAIL: README says {claimed_s.group(1)} scenarios and features/ "
            f"declares {scenarios}."
        )

if problems:
    print()
    print("A number on a README is a claim with no owner unless something re-reads")
    print("it. See CLAUDE.md invariant 9 and scripts/scenarios-have-tests.sh.")
    sys.exit(1)

print(f"OK: coverage {actual:.1f}% clears the README's floor; scenario count agrees.")
PY
