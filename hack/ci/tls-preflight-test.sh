#!/usr/bin/env bash

# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Offline tests for hack/ci/tls-preflight.sh: no network, no containers, no
# openssl. Exercises the config parser, the image reference validation and
# the result classifier, including the real panic output captured from the
# known-broken aks/command/runtime digest of AROSLSRE-2481. The full
# handshake check itself needs a container runtime and is validated by the
# tls-preflight Prow job instead.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=hack/ci/tls-preflight.sh
source "${ROOT}/hack/ci/tls-preflight.sh"

DATA="${ROOT}/hack/testdata/tls-preflight"
FAILURES=0
TEMP_FILES=()
trap 'rm -f "${TEMP_FILES[@]}"' EXIT

# mktemp registered for removal on exit, so a failed run leaves nothing in
# /tmp either. The name is assigned by printf -v rather than by command
# substitution: inside a subshell the array append would be lost and the
# file would leak.
new_temp_file() {
  local var="$1" file
  file=$(mktemp)
  TEMP_FILES+=("$file")
  printf -v "$var" '%s' "$file"
}

check() {
  local got="$1" want="$2" label="$3"
  if [[ "$got" == "$want" ]]; then
    echo "ok   ${label}"
  else
    echo "FAIL ${label}: want [${want}] got [${got}]" >&2
    FAILURES=$((FAILURES + 1))
  fi
}

check_fail() {
  local label="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "FAIL ${label}: expected non-zero exit" >&2
    FAILURES=$((FAILURES + 1))
  else
    echo "ok   ${label}"
  fi
}

# --- parser: synthetic fixtures ------------------------------------------

got=$(extract_image_field aksCommandRuntime registry "$DATA/config.yaml")
check "$got" "mcr.microsoft.com" "parser: aksCommandRuntime registry"

got=$(extract_image_field aksCommandRuntime repository "$DATA/config.yaml")
check "$got" "aks/command/runtime" "parser: aksCommandRuntime repository"

got=$(extract_image_field aksCommandRuntime digest "$DATA/config.yaml")
check "$got" "sha256:37df117f1179d78fd372fd35878d53ff30dfb350fb571fb197f3a2bee48bd157" "parser: aksCommandRuntime digest (trailing comment stripped)"

got=$(extract_image_field aksCommandRuntime resources "$DATA/config.yaml" && echo unexpected || echo absent)
check "$got" "absent" "parser: sibling keys after the component block are not read"

check_fail "parser: nested image fields are not picked up as flat fields" \
  extract_image_field arobit registry "$DATA/config.yaml"

check_fail "parser: missing component fails" \
  extract_image_field nonexistent digest "$DATA/config.yaml"

check_fail "parser: component without digest fails" \
  extract_image_field digestless digest "$DATA/config.yaml"

# --- parser: live repo config --------------------------------------------

got=$(extract_image_field aksCommandRuntime digest "${ROOT}/config/config.yaml")
if [[ "$got" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "ok   parser: live config yields a well-formed digest (${got:0:19}...)"
else
  echo "FAIL parser: live config digest is malformed: [${got}]" >&2
  FAILURES=$((FAILURES + 1))
fi

# --- image reference validation ------------------------------------------

TLS_PREFLIGHT_CONFIG="$DATA/config.yaml"
got=$(image_ref aksCommandRuntime)
check "$got" "mcr.microsoft.com/aks/command/runtime@sha256:37df117f1179d78fd372fd35878d53ff30dfb350fb571fb197f3a2bee48bd157" "image_ref: composes registry/repository@digest"

check_fail "image_ref: component without digest fails" image_ref digestless
check_fail "image_ref: missing component fails" image_ref nonexistent

# --- classifier -----------------------------------------------------------

got=$(classify_result 2 "$DATA/panic-broken-runtime.txt")
check "$got" "panic" "classifier: real AROSLSRE-2481 panic output is detected"

got=$(classify_result 0 "$DATA/http-success.txt")
check "$got" "pass" "classifier: TLS 1.3 server response passes"

new_temp_file first_pull
printf 'Trying to pull candidate image...\n' >"$first_pull"
cat "$DATA/http-success.txt" >>"$first_pull"
got=$(classify_result 0 "$first_pull")
check "$got" "pass" "classifier: successful first pull with TLS 1.3 response passes"

got=$(classify_result 0 "$DATA/connection-refused.txt")
check "$got" "inconclusive" "classifier: exit 0 without server response cannot pass"

got=$(classify_result 1 "$DATA/http-error.txt")
check "$got" "inconclusive" "classifier: unverified HTTP-like error cannot pass"

got=$(classify_result 125 "$DATA/pull-error.txt")
check "$got" "runtime-error" "classifier: podman pull failure (rc 125) is a runtime error"

got=$(classify_result 1 "$DATA/pull-error.txt")
check "$got" "runtime-error" "classifier: pull-failure phrasing is a runtime error even without rc 125"

got=$(classify_result 1 "$DATA/connection-refused.txt")
check "$got" "inconclusive" "classifier: connection refused is inconclusive"

got=$(classify_result 124 "$DATA/http-success.txt")
check "$got" "inconclusive" "classifier: timeout rc is inconclusive"

got=$(classify_result 1 "$DATA/unknown-flag.txt")
check "$got" "harness-error" "classifier: unknown flag is a harness error"

# A docker/podman pull failure contains bare HTTP status words ("403
# Forbidden"); those must not be reported as a pass. Only kubectl-worded
# errors or exit 0 decide a pass.
new_temp_file pull_words
sed 's/error: You must be logged in to the server (Unauthorized)/Unable to find image: 403 Forbidden/' \
  "$DATA/http-error.txt" >"$pull_words"
got=$(classify_result 125 "$pull_words")
check "$got" "runtime-error" "classifier: bare status words in a pull failure are not a pass"

# ---------------------------------------------------------------------------

if [[ "$FAILURES" -ne 0 ]]; then
  echo "${FAILURES} tls-preflight test(s) failed" >&2
  exit 1
fi
echo "all tls-preflight tests passed"
