#!/usr/bin/env bash
set -euo pipefail

# QA Validation Script for qa-agent
# Runs the full validation pipeline: build, unit tests, CLI smoke tests, integration tests

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORK_DIR=$(mktemp -d "${TMPDIR}/qa-agent-validate.XXXXXX")
BINARY="$WORK_DIR/qa-agent"
PASS=0
FAIL=0
TOTAL=0

cleanup() {
    rm -rf "$WORK_DIR"
}
trap cleanup EXIT

green()  { printf "\033[32m%s\033[0m\n" "$1"; }
red()    { printf "\033[31m%s\033[0m\n" "$1"; }
yellow() { printf "\033[33m%s\033[0m\n" "$1"; }

check() {
    local name="$1"
    shift
    TOTAL=$((TOTAL + 1))
    if "$@" >/dev/null 2>&1; then
        PASS=$((PASS + 1))
        green "  [PASS] $name"
    else
        FAIL=$((FAIL + 1))
        red "  [FAIL] $name"
    fi
}

check_output() {
    local name="$1"
    local expected="$2"
    shift 2
    TOTAL=$((TOTAL + 1))
    local output
    output=$("$@" 2>&1) || true
    if echo "$output" | grep -q "$expected"; then
        PASS=$((PASS + 1))
        green "  [PASS] $name"
    else
        FAIL=$((FAIL + 1))
        red "  [FAIL] $name (expected '$expected' in output)"
        echo "    got: $(echo "$output" | head -3)"
    fi
}

check_exit_code() {
    local name="$1"
    local want_code="$2"
    shift 2
    TOTAL=$((TOTAL + 1))
    local got_code=0
    "$@" >/dev/null 2>&1 || got_code=$?
    if [ "$got_code" -eq "$want_code" ]; then
        PASS=$((PASS + 1))
        green "  [PASS] $name"
    else
        FAIL=$((FAIL + 1))
        red "  [FAIL] $name (exit $got_code, want $want_code)"
    fi
}

echo "========================================"
echo "  qa-agent QA Validation Suite"
echo "  $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
echo "========================================"
echo ""

# --------------------------------------------------
# Phase 1: Build
# --------------------------------------------------
echo "Phase 1: Build"
check "go build ./..." go build -o /dev/null "$PROJECT_DIR/..."
check "build binary" go build -o "$BINARY" "$PROJECT_DIR/cmd/qa-agent"
echo ""

# --------------------------------------------------
# Phase 2: Unit Tests
# --------------------------------------------------
echo "Phase 2: Unit Tests (go test ./...)"
TOTAL=$((TOTAL + 1))
if go test "$PROJECT_DIR/..." -count=1 -timeout 120s 2>&1 | tail -5; then
    PASS=$((PASS + 1))
    green "  [PASS] all unit tests"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] unit tests had failures"
fi
echo ""

# --------------------------------------------------
# Phase 3: CLI Smoke Tests
# --------------------------------------------------
echo "Phase 3: CLI Binary Smoke Tests"

# Help
check_output "qa-agent --help" "Usage:" "$BINARY" --help
check_exit_code "qa-agent --help exits 0" 0 "$BINARY" --help

# Version
check_output "qa-agent --version" "0.1.0" "$BINARY" --version
check_exit_code "qa-agent --version exits 0" 0 "$BINARY" --version

# Unknown command
check_exit_code "qa-agent unknown exits 1" 1 "$BINARY" unknown

# Run command
RUN_OUTPUT_DIR="$WORK_DIR/runs"
mkdir -p "$RUN_OUTPUT_DIR"

check_output "qa-agent run creates run" "run_id:" "$BINARY" run --feature "Test feature" --surfaces web --output-dir "$RUN_OUTPUT_DIR"

# Capture the run_id for subsequent commands
RUN_ID=$("$BINARY" run --feature "Another test. Second sentence." --surfaces web,api --output-dir "$RUN_OUTPUT_DIR" 2>&1 | grep "^run_id:" | awk '{print $2}')

TOTAL=$((TOTAL + 1))
if [ -n "$RUN_ID" ]; then
    PASS=$((PASS + 1))
    green "  [PASS] run_id captured: $RUN_ID"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] failed to capture run_id"
    RUN_ID="run_fallback"
fi

# Verify run.json was created
RUN_DIR="$RUN_OUTPUT_DIR/$RUN_ID"
TOTAL=$((TOTAL + 1))
if [ -f "$RUN_DIR/run.json" ]; then
    PASS=$((PASS + 1))
    green "  [PASS] run.json exists"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] run.json missing at $RUN_DIR/run.json"
fi

# Verify run.json content
TOTAL=$((TOTAL + 1))
if python3 -c "import json; d=json.load(open('$RUN_DIR/run.json')); assert d['run_id']=='$RUN_ID'" 2>/dev/null; then
    PASS=$((PASS + 1))
    green "  [PASS] run.json has correct run_id"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] run.json content invalid"
fi

# Verify surfaces in run.json
TOTAL=$((TOTAL + 1))
if python3 -c "import json; d=json.load(open('$RUN_DIR/run.json')); assert d['surfaces']==['web','api']" 2>/dev/null; then
    PASS=$((PASS + 1))
    green "  [PASS] run.json has correct surfaces"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] run.json surfaces incorrect"
fi

# Run without --feature should fail
check_exit_code "qa-agent run without --feature exits 1" 1 "$BINARY" run --output-dir "$RUN_OUTPUT_DIR"

# Report command
check_output "qa-agent report generates files" "report:" "$BINARY" report --run-id "$RUN_ID" --output-dir "$RUN_OUTPUT_DIR"

TOTAL=$((TOTAL + 1))
if [ -f "$RUN_DIR/report.md" ]; then
    PASS=$((PASS + 1))
    green "  [PASS] report.md exists"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] report.md missing"
fi

TOTAL=$((TOTAL + 1))
if [ -f "$RUN_DIR/manifest.json" ]; then
    PASS=$((PASS + 1))
    green "  [PASS] manifest.json exists"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] manifest.json missing"
fi

# Bundle command
BUNDLE_PATH="$WORK_DIR/test-bundle.zip"
check_output "qa-agent bundle creates zip" "bundle:" "$BINARY" bundle --run-id "$RUN_ID" --output-dir "$RUN_OUTPUT_DIR" --out "$BUNDLE_PATH"

TOTAL=$((TOTAL + 1))
if [ -f "$BUNDLE_PATH" ] && python3 -c "import zipfile; z=zipfile.ZipFile('$BUNDLE_PATH'); assert len(z.namelist()) > 0" 2>/dev/null; then
    PASS=$((PASS + 1))
    green "  [PASS] bundle is valid zip with files"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] bundle invalid or empty"
fi

# Trace command (should show no traces since this was just a run init, no execution)
check_output "qa-agent trace lists traces" "no traces found" "$BINARY" trace --run-id "$RUN_ID" --output-dir "$RUN_OUTPUT_DIR"

# Report on non-existent run
check_exit_code "qa-agent report non-existent run exits 1" 1 "$BINARY" report --run-id "run_does_not_exist" --output-dir "$RUN_OUTPUT_DIR"

echo ""

# --------------------------------------------------
# Phase 4: Integration Tests (golden + adversarial + bug repros)
# --------------------------------------------------
echo "Phase 4: Integration Tests"
TOTAL=$((TOTAL + 1))
INT_OUTPUT=$(go test -v -count=1 -timeout 300s "$PROJECT_DIR/internal/integration/" -run "TestQA_|TestBug_" 2>&1) || true
INT_PASS=$(echo "$INT_OUTPUT" | grep -c "^--- PASS:" || true)
INT_FAIL=$(echo "$INT_OUTPUT" | grep -c "^--- FAIL:" || true)

if [ "$INT_FAIL" -eq 0 ]; then
    PASS=$((PASS + 1))
    green "  [PASS] all integration tests ($INT_PASS passed)"
else
    FAIL=$((FAIL + 1))
    red "  [FAIL] $INT_FAIL integration test(s) failed"
fi

# Print bug confirmation lines
echo ""
echo "Bug reproduction results:"
echo "$INT_OUTPUT" | grep "BUG B" | while read -r line; do
    if echo "$line" | grep -q "CONFIRMED"; then
        yellow "  $line"
    else
        green "  $line"
    fi
done

echo ""
echo "========================================"
echo "  Results: $PASS/$TOTAL passed, $FAIL failed"
echo "========================================"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
