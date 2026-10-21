#!/usr/bin/env bash
# Offline test harness for hack/istio-verify-state.sh.
#
# Runs `verify-file` against static fixtures (no cluster, no kubectl) and
# asserts the expected exit code. Also statically checks that mutating
# kubectl verbs only appear inside run_live_checks, so the default
# (non---live) code paths remain read-only.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$ROOT/hack/istio-verify-state.sh"
DATA="$ROOT/hack/testdata/istio-verify"

assert_exit() {
    local want="$1" file="$2" label="$3"
    set +e
    "$SCRIPT" verify-file "$file" >/tmp/istio-verify-test.out 2>&1
    local got=$?
    set -e
    if [ "$got" -ne "$want" ]; then
        echo "FAIL $label: want exit $want got $got"
        cat /tmp/istio-verify-test.out
        exit 1
    fi
    echo "ok $label"
}

assert_exit 0 "$DATA/happy.txt" happy
assert_exit 1 "$DATA/missing-shared-cm.txt" missing-shared-cm
assert_exit 1 "$DATA/cm-no-ext-authz.txt" cm-no-ext-authz
assert_exit 1 "$DATA/no-ingress-pip.txt" no-ingress-pip

# Read-only static check: mutating verbs only in run_live_checks
SCRIPT="$SCRIPT" python3 - <<'PY'
import os
from pathlib import Path
text = Path(os.environ["SCRIPT"]).read_text()
# Split on function definition; ensure mutating verbs outside live are absent
before, _, rest = text.partition("run_live_checks()")
live, _, after = rest.partition("\nrun_verification()")
import re
pat = re.compile(r"kubectl\s+(apply|create|run|delete|patch|replace|scale|rollout)\b")
bad = [m.group(0) for m in pat.finditer(before)] + [m.group(0) for m in pat.finditer(after)]
if bad:
    raise SystemExit(f"mutating kubectl outside run_live_checks: {bad}")
print("ok read-only-default")
PY

echo "All istio-verify-state tests passed"
