#!/usr/bin/env bash
# Runs this provider's TF_ACC-gated acceptance tests (TestAccBudgetResource,
# TestAccWardryxPolicyResource) against real, disposable, local instances of
# TokenFuse Cloud and Wardryx: builds and starts both from sibling repo
# checkouts, waits for them to answer /healthz, points the acceptance tests
# at them, runs `go test`, then always tears both processes down again --
# nothing here is meant to persist past one run.
#
# Sibling repo layout: defaults to ../tokenfuse and ../wardryx next to this
# repo's root (matching how the TAIPANBOX repos are normally checked out
# side by side); override with TOKENFUSE_REPO_DIR / WARDRYX_REPO_DIR for a
# different layout (this is also how CI points it at its own checkouts).
set -euo pipefail
# Job control on, deliberately: it puts each `... &` background pipeline
# below in its own process group, so cleanup() can kill the whole group
# (subshell + cargo/go build wrapper + the actual server binary it execs)
# with one `kill -- -$PID`. Without this, killing just $! leaves cargo's
# and wardryx's actual server processes running, reparented to init --
# confirmed the hard way the first time this script ran.
set -m

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SIBLING_DIR="$(cd "$REPO_ROOT/.." && pwd)"

TOKENFUSE_REPO_DIR="${TOKENFUSE_REPO_DIR:-$SIBLING_DIR/tokenfuse}"
WARDRYX_REPO_DIR="${WARDRYX_REPO_DIR:-$SIBLING_DIR/wardryx}"

CLOUD_PORT="${TESTACC_CLOUD_PORT:-18080}"
WARDRYX_PORT="${TESTACC_WARDRYX_PORT:-18090}"
TEST_KEY="taipan-acctest"
TEST_ORG="taipan-acctest-org"

if [ ! -d "$TOKENFUSE_REPO_DIR" ]; then
  echo "error: TOKENFUSE_REPO_DIR ($TOKENFUSE_REPO_DIR) does not exist." >&2
  echo "  clone github.com/TAIPANBOX/tokenfuse next to this repo, or set TOKENFUSE_REPO_DIR." >&2
  exit 2
fi
if [ ! -d "$WARDRYX_REPO_DIR" ]; then
  echo "error: WARDRYX_REPO_DIR ($WARDRYX_REPO_DIR) does not exist." >&2
  echo "  clone github.com/TAIPANBOX/wardryx next to this repo, or set WARDRYX_REPO_DIR." >&2
  exit 2
fi

LOG_DIR="$(mktemp -d)"
echo "logs: $LOG_DIR"

CLOUD_PID=""
WARDRYX_PID=""
cleanup() {
  local status=$?
  echo "--- tearing down local test backends ---"
  [ -n "$CLOUD_PID" ] && kill -TERM -- "-$CLOUD_PID" 2>/dev/null || true
  [ -n "$WARDRYX_PID" ] && kill -TERM -- "-$WARDRYX_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM

wait_healthy() {
  local name="$1" url="$2" tries=60
  echo "waiting for $name at $url ..."
  while [ "$tries" -gt 0 ]; do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      echo "$name is up"
      return 0
    fi
    tries=$((tries - 1))
    sleep 1
  done
  echo "error: $name never became healthy at $url; see its log in $LOG_DIR" >&2
  return 1
}

echo "--- starting TokenFuse Cloud (cargo run -p tokenfuse-cloud, port $CLOUD_PORT) ---"
(
  cd "$TOKENFUSE_REPO_DIR"
  TOKENFUSE_CLOUD_KEYS="$TEST_KEY:$TEST_ORG:admin" PORT="$CLOUD_PORT" \
    cargo run -q -p tokenfuse-cloud
) > "$LOG_DIR/tokenfuse-cloud.log" 2>&1 &
CLOUD_PID=$!

echo "--- building and starting Wardryx (port $WARDRYX_PORT) ---"
(cd "$WARDRYX_REPO_DIR" && make build) > "$LOG_DIR/wardryx-build.log" 2>&1
(
  cd "$WARDRYX_REPO_DIR"
  WARDRYX_KEYS="$TEST_KEY:$TEST_ORG:admin" \
    ./bin/wardryx serve -addr ":$WARDRYX_PORT"
) > "$LOG_DIR/wardryx.log" 2>&1 &
WARDRYX_PID=$!

wait_healthy "TokenFuse Cloud" "http://127.0.0.1:$CLOUD_PORT/healthz"
wait_healthy "Wardryx" "http://127.0.0.1:$WARDRYX_PORT/healthz"

echo "--- running acceptance tests ---"
export TF_ACC=1
export TOKENFUSE_CLOUD_URL="http://127.0.0.1:$CLOUD_PORT"
export TOKENFUSE_CLOUD_KEY="$TEST_KEY"
export WARDRYX_URL="http://127.0.0.1:$WARDRYX_PORT"
export WARDRYX_KEY="$TEST_KEY"

cd "$REPO_ROOT"
go test ./internal/provider/... -run '^TestAcc' -v -timeout 20m
