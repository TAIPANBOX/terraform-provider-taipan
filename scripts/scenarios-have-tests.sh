#!/usr/bin/env bash
# Binds features/*.feature to the tests that hold them.
#
# The scenarios exist so a reader can check what this provider promises without
# reading Go. That only works while every scenario is actually run: a scenario
# nobody runs is a nicer-looking comment, and it rots the same way, only with
# more authority because it reads like a specification.
#
# Two directions, both checked:
#   1. every Scenario carries a @test: tag;
#   2. every @test: tag names a `func Test...` that exists in a _test.go file.
#
# The reverse of 2 is deliberately NOT checked. A unit test without a scenario
# is fine and common: not every assertion is a promise to an operator, and a
# gate demanding one scenario per test gets scenarios deleted to keep it quiet.
#
# WHAT A SCENARIO HERE IS AND IS NOT ALLOWED TO CLAIM
#
# CLAUDE.md: "A green unit-test run means very little in a Terraform provider."
# A scenario bound to a unit test may claim things about schema shape and
# mapping. It may not claim a resource works. Only the acceptance suite can say
# that, and this gate cannot tell the two apart, so the distinction stays a
# matter for review.
#
# This file is the ONE copy of this check. The local run and CI both call it.

set -euo pipefail

cd "$(dirname "$0")/.."

python3 - <<'PY'
import pathlib
import re
import sys

SCENARIO = re.compile(r"^\s*Scenario:\s*(.+?)\s*$")
TAG = re.compile(r"^\s*@test:([A-Za-z_][A-Za-z0-9_]*)\s*$")
TEST_FN = re.compile(r"^func (Test[A-Za-z0-9_]*)\s*\(")

fail = False

# The subject first. A glob over a directory that is not there yields nothing,
# and a check that read no file must never report that everything it did not
# read was fine.
features = sorted(pathlib.Path("features").glob("*.feature"))
if not features:
    print("FAIL: no .feature file under features/, so this measured nothing.")
    print("      It cannot say every scenario is held by a test if it read none.")
    print("      If the scenarios moved, this check has to move with them.")
    sys.exit(1)

sources = sorted(pathlib.Path(".").rglob("*_test.go"))
if not sources:
    print("FAIL: no _test.go file anywhere, so this measured nothing.")
    print("      Every tag would look unresolved, which is a different fault")
    print("      wearing the same message.")
    sys.exit(1)

known_tests = set()
for path in sources:
    for line in path.read_text().splitlines():
        m = TEST_FN.match(line)
        if m:
            known_tests.add(m.group(1))

if not known_tests:
    print("FAIL: no `func Test...` found in any _test.go, so this measured nothing.")
    sys.exit(1)

tagged = 0
for path in features:
    pending_tag = None
    for lineno, line in enumerate(path.read_text().splitlines(), 1):
        m = TAG.match(line)
        if m:
            pending_tag = (m.group(1), lineno)
            continue
        m = SCENARIO.match(line)
        if not m:
            continue
        scenario = m.group(1)
        if pending_tag is None:
            print(f"FAIL: {path}:{lineno}: scenario has no @test: tag above it")
            print(f"      {scenario}")
            fail = True
            continue
        name, tag_line = pending_tag
        tagged += 1
        if name not in known_tests:
            print(f"FAIL: {path}:{tag_line}: @test:{name} names no Go test that exists")
            print(f"      scenario: {scenario}")
            fail = True
        pending_tag = None

if fail:
    print()
    print("A scenario nobody runs is a comment that reads like a specification.")
    print("Either write the test and name it in the tag, or delete the scenario.")
    sys.exit(1)

print(
    f"OK: {tagged} scenario(s) across {len(features)} feature file(s), "
    f"every one bound to a test that exists."
)
PY
