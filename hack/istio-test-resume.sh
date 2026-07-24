#!/bin/bash
# Automated istio upgrade resume testing via make personal-dev-env.
#
# Cycles through kill points, killing and resuming at each step to validate
# that the upgrade recovers correctly from any interruption point.
#
# Usage:
#   ./hack/istio-test-resume.sh                        # full cycle, builds images
#   ./hack/istio-test-resume.sh --skip-build            # skip image build (use existing)
#   ./hack/istio-test-resume.sh --skip-cache            # bypass pipeline step caching
#   ./hack/istio-test-resume.sh --skip-build --skip-cache --steps tag,orphan  # combined
#
# What it does:
#   1. Runs make personal-dev-env (detects install vs upgrade)
#   2. For installs: lets the pipeline complete, reports success
#   3. For upgrades: kills after the first kill point, verifies, re-runs
#   4. Repeats kill/resume for each configured step
#   5. Final run completes the upgrade end-to-end
#
# Kill points (upgrade/resume/cleanup-and-upgrade — matched to log messages
# from ARO-Tools/tools/istio-upgrade/pkg/istio/):
#   canary     - ARM canary started, two CPs installing
#   configmap  - MISE ConfigMap created or updated for target revision
#   tag        - Tag webhook created or flipped to new istiod
#   migrate    - Workloads restarted with new sidecars
#   health     - Health check passed, before orphan guard
#   orphan     - Orphan check passed, before ARM complete
#
# WARNING: ARM complete and cleanup are after the point of no return — never killed.
#
# Notes:
#   - After rebuilding pers-dev, refresh kubeconfig first:
#     e.g az aks get-credentials -g hcp-underlay-pers-usw3trwi-svc -n pers-usw3trwi-svc --overwrite-existing
#   - Killing during an ARM operation (canary start/complete) leaves the cluster
#     in "Updating" state. The next run will get ActionSkip until provisioning
#     finishes (~3-5 min). The script waits automatically.
#   - The pipeline runs istio-upgrade concurrently with image mirror steps.
#     If image mirror fails (e.g. VPN/DNS), context cancellation kills istio too.
#     Use --skip-build to minimize concurrent step failures.
#
# Prerequisites:
#   - az login (active session)
#   - kubeconfig pointing at pers-dev svc cluster
#   - Run from ARO-HCP repo root

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RESULTS_DIR="/tmp/istio-resume-test"

# Archive previous run if it exists
if [[ -d "${RESULTS_DIR}" ]]; then
  ARCHIVE="${RESULTS_DIR}-$(date -r "${RESULTS_DIR}" +%Y%m%d-%H%M%S 2>/dev/null || stat -c %Y "${RESULTS_DIR}" 2>/dev/null || echo 'old')"
  mv "${RESULTS_DIR}" "${ARCHIVE}"
  echo "Archived previous run to ${ARCHIVE}"
fi
mkdir -p "${RESULTS_DIR}"

# Safety: ensure we're targeting a personal-dev environment
DEPLOY_ENV="${DEPLOY_ENV:-pers}"
if [[ "$DEPLOY_ENV" != "pers" && "$DEPLOY_ENV" != "swft" ]]; then
  echo "ERROR: DEPLOY_ENV=${DEPLOY_ENV} — this script only runs against personal-dev (pers/swft)."
  exit 1
fi
export DEPLOY_ENV

# Default kill points — the interesting ones for resume testing
DEFAULT_STEPS="canary,tag,migrate,orphan"
SKIP_BUILD=false
SKIP_CACHE=false

# Parse arguments
STEPS="${DEFAULT_STEPS}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --steps) STEPS="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --skip-cache) SKIP_CACHE=true; shift ;;
    --help|-h)
      sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

IFS=',' read -ra KILL_POINTS <<< "$STEPS"

# Validate kill points
for kp in "${KILL_POINTS[@]}"; do
  case "$kp" in
    canary|configmap|tag|migrate|health|orphan) ;;
    *) echo "ERROR: Invalid kill point '${kp}'. Use: canary, configmap, tag, migrate, health, orphan"; exit 1 ;;
  esac
done

echo "Target environment: DEPLOY_ENV=${DEPLOY_ENV}, USER=${USER}"
if [[ "$SKIP_BUILD" == "true" ]]; then
  echo "Image build:       SKIPPED (using existing images)"
  export USE_LATEST_IMAGES=1
fi
if [[ "$SKIP_CACHE" == "true" ]]; then
  echo "Pipeline cache:    SKIPPED (forcing re-execution of all steps)"
  export SKIP_CACHE=1
fi

pattern_for_step() {
  # Patterns matched against actual pipeline log output (ANSI-stripped).
  # Must match log messages in ARO-Tools/tools/istio-upgrade/pkg/istio/:
  #   upgrade.go   — orchestrator step logs
  #   configmap.go — "ConfigMap created" / "ConfigMap updated"
  #   webhooks.go  — "Created revision tag webhook" / "Updated revision tag webhook"
  #   workloads.go — "Restarted workloads with stale sidecars" / "All workloads ready"
  #   health.go    — health check results (via upgrade.go "Health check passed")
  case "$1" in
    canary)    echo "Starting canary" ;;
    configmap) echo "ConfigMap created\|ConfigMap updated" ;;
    tag)       echo "Updated revision tag webhook\|Created revision tag webhook" ;;
    migrate)   echo "Restarted workloads with stale sidecars\|All workloads ready" ;;
    health)    echo "Health check passed" ;;
    orphan)    echo "No orphaned workloads" ;;
  esac
}

label_for_step() {
  case "$1" in
    canary)    echo "ARM canary started (two CPs installing)" ;;
    configmap) echo "MISE ConfigMap created/updated" ;;
    tag)       echo "Tag webhook created/updated for new istiod" ;;
    migrate)   echo "Workloads migrated to new sidecars" ;;
    health)    echo "Health check passed" ;;
    orphan)    echo "Orphan check passed (before ARM complete)" ;;
  esac
}

kill_tree() {
  local pid=$1
  local sig="${2:-TERM}"
  local children
  children=$(pgrep -P "$pid" 2>/dev/null) || true
  for child in $children; do
    kill_tree "$child" "$sig"
  done
  kill -"$sig" "$pid" 2>/dev/null || true
}

kill_pipeline() {
  local pid=$1
  if [[ "$(uname)" == "Linux" ]]; then
    local pgid
    pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ')
    if [[ -n "$pgid" ]]; then
      kill -- -"$pgid" 2>/dev/null || true
    fi
  else
    kill_tree "$pid" TERM
  fi
  for _ in $(seq 1 10); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.5
  done
  if [[ "$(uname)" == "Linux" ]]; then
    local pgid
    pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ')
    [[ -n "$pgid" ]] && kill -9 -- -"$pgid" 2>/dev/null || true
  else
    kill_tree "$pid" KILL
  fi
}

# Detect the istio action from the log output.
# Check order matters:
#   1. resume before upgrade — resume logs also contain "Starting canary"
#   2. cleanup-and-upgrade before upgrade — cleanup also starts a canary in Phase 3
#   3. reconcile before skip — reconcile logs "Already at target" which is not a skip
#
# Text patterns must match decide.go log messages (case-insensitive via grep -qi):
#   resume:   "Mid-upgrade detected, resuming"
#   install:  "No revisions installed, installing from svc.istio.versions"
#   cleanup:  "Stale canary detected, will clean up and upgrade"
#   upgrade:  "Upgrading to svc.istio.versions target"
#   reconcile:"Already at target -- reconciling expected resource state"
#   skip:     "Skipping: ..." / "Downgrade detected, skipping" / "...upgrades, skipping"
detect_action() {
  local logfile="$1"
  local stripped
  stripped=$(LC_ALL=C sed 's/\x1b\[[0-9;]*m//g' "$logfile" 2>/dev/null)
  if echo "$stripped" | grep -qi '"action".*"resume"\|Mid-upgrade detected'; then
    echo "resume"
  elif echo "$stripped" | grep -qi '"action".*"install"\|Enabling mesh'; then
    echo "install"
  elif echo "$stripped" | grep -qi '"action".*"cleanup-and-upgrade"\|Stale canary detected'; then
    echo "cleanup-and-upgrade"
  elif echo "$stripped" | grep -qi '"action".*"upgrade"\|Starting canary'; then
    echo "upgrade"
  elif echo "$stripped" | grep -qi '"action".*"reconcile"\|reconciling expected'; then
    echo "reconcile"
  elif echo "$stripped" | grep -qi '"action".*"skip"\|Skipping:\|, skipping'; then
    echo "skip"
  else
    echo "unknown"
  fi
}

run_and_monitor() {
  local pattern="$1"
  local logfile="$2"
  local should_kill="$3"

  cd "${REPO_ROOT}"
  if [[ "$(uname)" == "Linux" ]]; then
    setsid make personal-dev-env > "${logfile}" 2>&1 &
  else
    make personal-dev-env > "${logfile}" 2>&1 &
  fi
  local make_pid=$!

  tail -f "${logfile}" 2>/dev/null &
  local tail_pid=$!

  if [[ "$should_kill" == "true" ]]; then
    while true; do
      # Strip ANSI escape codes before matching — the pipeline log formatter
      # wraps messages in color codes that break plain grep.
      if LC_ALL=C sed 's/\x1b\[[0-9;]*m//g' "$logfile" 2>/dev/null | grep -q "$pattern"; then
        sleep 2
        kill "$tail_pid" 2>/dev/null || true
        kill_pipeline "$make_pid"
        return 0
      fi

      if ! kill -0 "$make_pid" 2>/dev/null; then
        kill "$tail_pid" 2>/dev/null || true
        wait "$make_pid" 2>/dev/null
        return $?
      fi

      sleep 0.5
    done
  else
    wait "$make_pid"
    local exit_code=$?
    kill "$tail_pid" 2>/dev/null || true
    return "$exit_code"
  fi
}

echo "=============================================="
echo "  Istio Upgrade Resume Test"
echo "=============================================="
echo ""
echo "Kill points to test: ${KILL_POINTS[*]}"
echo "Results directory:   ${RESULTS_DIR}"
echo ""
echo "=============================================="

# --- Round 1: Initial run to detect install vs upgrade ---
ROUND=1
LOGFILE="${RESULTS_DIR}/round-1-initial.log"
FIRST_PATTERN=$(pattern_for_step "${KILL_POINTS[0]}")
FIRST_LABEL=$(label_for_step "${KILL_POINTS[0]}")

echo ""
echo "----------------------------------------------"
echo "  Round 1: Initial pipeline run"
echo "  Watching for: ${FIRST_LABEL}"
echo "  Log: ${LOGFILE}"
echo "----------------------------------------------"

run_and_monitor "$FIRST_PATTERN" "$LOGFILE" "true"
INITIAL_EXIT=$?
ACTION=$(detect_action "$LOGFILE")

echo ""
echo ">>> Detected action: ${ACTION}"

PASSED=0
FAILED=0

case "$ACTION" in
  install)
    echo ">>> Fresh install detected — no canary, no resume to test."
    echo ">>> Letting pipeline complete..."
    LOGFILE="${RESULTS_DIR}/round-1-install-complete.log"
    if run_and_monitor "" "$LOGFILE" "false"; then
      echo ">>> PASS — fresh install completed successfully"
      PASSED=1
    else
      echo ">>> FAIL — fresh install failed (exit $?)"
      echo ">>> Check log: ${LOGFILE}"
      FAILED=1
    fi
    ;;

  skip|reconcile)
    echo ">>> Already at target version or cluster still provisioning."
    echo ">>> If cluster was killed mid-ARM, wait for provisioning to finish and re-run."
    echo ">>> Pipeline completed normally. No resume testing needed this round."
    PASSED=1
    ;;

  upgrade|resume|cleanup-and-upgrade)
    echo ">>> Upgrade/canary flow detected (${ACTION}) — running kill/resume cycle."

    # Check if upgrade completed instead of being killed
    UPGRADE_DONE=false
    if LC_ALL=C sed 's/\x1b\[[0-9;]*m//g' "$LOGFILE" 2>/dev/null | grep -q "Istio upgrade complete and verified"; then
      echo ">>> Upgrade completed on first run (kill point came too late)."
      echo ">>> No resume testing needed."
      PASSED=1
      UPGRADE_DONE=true
    fi

    if [[ "$UPGRADE_DONE" == "true" ]]; then
      : # skip to summary
    elif [[ "$INITIAL_EXIT" -eq 0 ]]; then
      echo ">>> KILLED after: ${FIRST_LABEL}"
      PASSED=$((PASSED + 1))
    else
      echo ">>> Pipeline exited (code ${INITIAL_EXIT}) before kill point."
      echo ">>> Check log: ${LOGFILE}"
      FAILED=$((FAILED + 1))
    fi

    if [[ "$UPGRADE_DONE" != "true" ]]; then
    # Run verify after first kill
    if [[ -x "${SCRIPT_DIR}/istio-verify-state.sh" ]]; then
      "${SCRIPT_DIR}/istio-verify-state.sh" > "${RESULTS_DIR}/round-1-verify.log" 2>&1 || true
    fi

    echo ""
    echo ">>> Waiting 5s before next round..."
    sleep 5

    # Remaining kill points (skip the first, already done)
    for i in $(seq 1 $((${#KILL_POINTS[@]} - 1))); do
      kp="${KILL_POINTS[$i]}"
      ROUND=$((ROUND + 1))
      LOGFILE="${RESULTS_DIR}/round-${ROUND}-kill-${kp}.log"
      PATTERN=$(pattern_for_step "$kp")
      LABEL=$(label_for_step "$kp")

      echo ""
      echo "----------------------------------------------"
      echo "  Round ${ROUND}: Kill after ${LABEL}"
      echo "  Log: ${LOGFILE}"
      echo "----------------------------------------------"

      run_and_monitor "$PATTERN" "$LOGFILE" "true"
      EXIT_CODE=$?
      RESUME_ACTION=$(detect_action "$LOGFILE")
      echo ""
      echo ">>> Action on re-run: ${RESUME_ACTION}"

      # Check if the upgrade completed instead of being killed
      if LC_ALL=C sed 's/\x1b\[[0-9;]*m//g' "$LOGFILE" 2>/dev/null | grep -q "Istio upgrade complete and verified"; then
        echo ">>> Upgrade completed during this round (pattern matched after completion)."
        echo ">>> No more kill points to test."
        PASSED=$((PASSED + 1))
        UPGRADE_DONE=true
        break
      fi

      if [[ "$RESUME_ACTION" == "skip" || "$RESUME_ACTION" == "reconcile" ]]; then
        echo ">>> ${RESUME_ACTION} — upgrade already completed. Stopping test."
        PASSED=$((PASSED + 1))
        UPGRADE_DONE=true
        break
      fi

      if [[ "$EXIT_CODE" -eq 0 ]]; then
        echo ">>> KILLED after: ${LABEL}"

        if [[ -x "${SCRIPT_DIR}/istio-verify-state.sh" ]]; then
          "${SCRIPT_DIR}/istio-verify-state.sh" > "${RESULTS_DIR}/round-${ROUND}-verify-${kp}.log" 2>&1 || true
        fi

        PASSED=$((PASSED + 1))
      else
        echo ">>> UNEXPECTED — pipeline exited (code ${EXIT_CODE}) before '${PATTERN}'"
        echo ">>> Check log: ${LOGFILE}"
        FAILED=$((FAILED + 1))
      fi

      echo ""
      echo ">>> Waiting 5s before next round..."
      sleep 5
    done

    # Final run — let it complete
    ROUND=$((ROUND + 1))
    LOGFILE="${RESULTS_DIR}/round-${ROUND}-complete.log"

    echo ""
    echo "----------------------------------------------"
    echo "  Round ${ROUND}: Final run (complete upgrade)"
    echo "  Log: ${LOGFILE}"
    echo "----------------------------------------------"

    if run_and_monitor "" "$LOGFILE" "false"; then
      FINAL_ACTION=$(detect_action "$LOGFILE")
      echo ""
      echo ">>> Action on final run: ${FINAL_ACTION}"
      echo ">>> PASS — upgrade completed successfully"
      PASSED=$((PASSED + 1))
    else
      EXIT_CODE=$?
      echo ""
      echo ">>> FAIL — upgrade failed with exit code ${EXIT_CODE}"
      echo ">>> Check log: ${LOGFILE}"
      FAILED=$((FAILED + 1))
    fi
    fi # UPGRADE_DONE guard
    ;;

  unknown)
    echo ">>> Could not detect istio action from log."
    echo ">>> The istio-upgrade step may not have been reached."
    echo ">>> Check log: ${LOGFILE}"
    FAILED=1
    ;;
esac

# Summary
echo ""
echo "=============================================="
echo "  Results"
echo "=============================================="
echo ""
echo "  Rounds: ${ROUND}"
echo "  Passed: ${PASSED}"
echo "  Failed: ${FAILED}"
echo "  Logs:   ${RESULTS_DIR}/"
echo ""

ls -1 "${RESULTS_DIR}/"

echo ""
if [[ "$FAILED" -eq 0 ]]; then
  echo "ALL ROUNDS PASSED"
  exit 0
else
  echo "SOME ROUNDS FAILED — review logs above"
  exit 1
fi
