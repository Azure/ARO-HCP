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

# TLS 1.3 preflight for deployment images that ship an executable kubectl.
#
# The mcr.microsoft.com/aks/command/runtime image (config component
# aksCommandRuntime) provides the kubectl that deployment Jobs execute
# directly: unannotate-default-sc (mgmt clusters), finalize-mce (svc
# clusters), the velero install Jobs, and the arobit cert-watchdog sidecar.
# A broken Go/OpenSSL FIPS integration in that kubectl panics on any TLS 1.3
# handshake (AROSLSRE-2021, AROSLSRE-2481) and only surfaces days later as
# e2e-parallel provision failures. This script makes the kubectl inside the
# candidate image perform one real TLS 1.3 handshake against a local,
# TLS-1.3-only openssl s_server, so a broken digest fails on the bump PR
# itself in about a minute (AROSLSRE-2483).
#
# curl/wget cannot catch this class: they use their own TLS stack, not
# kubectl's. The kubectl binary must run inside the image because it
# dynamically links the image's own OpenSSL (msft-golang); extracting it and
# running it on the runner does not reproduce the failure.
#
# Components to check are listed in TLS_PREFLIGHT_COMPONENTS (default:
# aksCommandRuntime). Each must have a flat defaults.<component>.image
# {registry, repository, digest} entry in config/config.yaml. The
# first-party kubectl image (AROSLSRE-2528) is covered by adding its
# component name to that list once it lands in config.
#
# In presubmits (PULL_BASE_SHA set and present in the local clone) a
# component is only checked when its digest changed relative to the base
# commit, so unrelated config/config.yaml edits cost nothing. Without a
# usable base (periodics, rehearsals, local runs) every listed component is
# checked unconditionally.
#
# The check fails closed: a kubectl panic fails immediately, and an image
# that cannot be verified after TLS_PREFLIGHT_ATTEMPTS attempts also fails,
# so an unverified digest cannot merge.
#
# Environment:
#   TLS_PREFLIGHT_COMPONENTS  space-separated config components
#                             (default: aksCommandRuntime)
#   TLS_PREFLIGHT_CONFIG      config.yaml path, relative to the repo root
#                             (default: config/config.yaml)
#   TLS_PREFLIGHT_PORT        local port for the TLS-1.3-only server
#                             (default: 8443)
#   TLS_PREFLIGHT_ATTEMPTS    kubectl attempts per image (default: 3)
#   TLS_PREFLIGHT_TIMEOUT     seconds per kubectl attempt (default: 120)
#   PULL_BASE_SHA             Prow base commit for the presubmit diff gate
#
# Requires: podman or docker, openssl, git, timeout.
# Called by the aro-hcp-tls-preflight step-registry wrapper in
# openshift/release; can also be run locally from the repo root.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

TLS_PREFLIGHT_COMPONENTS="${TLS_PREFLIGHT_COMPONENTS:-aksCommandRuntime}"
TLS_PREFLIGHT_CONFIG="${TLS_PREFLIGHT_CONFIG:-config/config.yaml}"
TLS_PREFLIGHT_PORT="${TLS_PREFLIGHT_PORT:-8443}"
TLS_PREFLIGHT_ATTEMPTS="${TLS_PREFLIGHT_ATTEMPTS:-3}"
TLS_PREFLIGHT_TIMEOUT="${TLS_PREFLIGHT_TIMEOUT:-120}"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

# Extract one image field (registry, repository or digest) for a component
# from config/config.yaml. Components are two-space-indented keys under
# defaults:, their image fields six-space-indented keys of the flat
# defaults.<component>.image block. Prints the value and returns 0, or
# returns 1 when the component block or the field is absent.
extract_image_field() {
  local component="$1" field="$2" file="$3" value
  value=$(awk -v component="$component" -v field="$field" '
    index($0, "  " component ":") == 1 { in_block = 1; next }
    in_block && /^  [A-Za-z]/ { exit }
    in_block && /^      [A-Za-z]/ && $1 == field ":" {
      value = $2
      gsub(/^"|"$/, "", value)
      print value
      exit
    }
  ' "$file")
  [[ -n "$value" ]] || return 1
  printf '%s\n' "$value"
}

# Print registry/repository@digest for a component, validating the pieces.
# Returns non-zero (with a message on stderr) when the component or any of
# its image fields is missing or malformed.
image_ref() {
  local component="$1" registry repository digest
  registry=$(extract_image_field "$component" registry "$TLS_PREFLIGHT_CONFIG") || {
    echo "component ${component} has no defaults.${component}.image.registry in ${TLS_PREFLIGHT_CONFIG}" >&2
    return 1
  }
  repository=$(extract_image_field "$component" repository "$TLS_PREFLIGHT_CONFIG") || {
    echo "component ${component} has no defaults.${component}.image.repository in ${TLS_PREFLIGHT_CONFIG}" >&2
    return 1
  }
  digest=$(extract_image_field "$component" digest "$TLS_PREFLIGHT_CONFIG") || {
    echo "component ${component} has no defaults.${component}.image.digest in ${TLS_PREFLIGHT_CONFIG}" >&2
    return 1
  }
  [[ "$registry" =~ ^[a-zA-Z0-9.-]+(:[0-9]+)?$ ]] || {
    echo "component ${component} has an invalid registry: ${registry}" >&2
    return 1
  }
  [[ "$repository" =~ ^[a-zA-Z0-9./_-]+$ ]] || {
    echo "component ${component} has an invalid repository: ${repository}" >&2
    return 1
  }
  [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || {
    echo "component ${component} has an invalid digest: ${digest}" >&2
    return 1
  }
  printf '%s/%s@%s\n' "$registry" "$repository" "$digest"
}

# Classify one kubectl invocation. $1 is the exit code of the container
# runtime invocation, $2 the file holding the combined output (read with
# grep, never with shell substitution: Go panic traces contain null bytes).
# Prints exactly one verdict:
#   pass           TLS 1.3 handshake completed (kubectl reached the HTTP layer)
#   panic          the AROSLSRE-2021/2481 regression signature
#   runtime-error  container runtime failed (e.g. image pull); knows nothing
#                  about kubectl and must never be reported as a pass
#   harness-error  kubectl rejected the check invocation itself
#   inconclusive   connection-level result; retry, then fail closed
classify_result() {
  local rc="$1" output_file="$2"
  if grep -q 'panic:\|OSSL_PARAM' "$output_file"; then
    echo panic
    return
  fi
  if [[ "$rc" -eq 125 || "$rc" -eq 126 || "$rc" -eq 127 ]] \
    || grep -q '^Trying to pull \|Unable to find image\|Error response from daemon' "$output_file"; then
    echo runtime-error
    return
  fi
  if [[ "$rc" -eq 0 ]]; then
    echo pass
    return
  fi
  # These kubectl-worded errors can only be produced after a response was
  # received and decoded, so the handshake completed. Docker/podman pull
  # errors contain bare status words instead and are already classified
  # above; generic "error:" prefixes are deliberately not accepted.
  if grep -q 'Error from server\|the server has asked\|You must be logged in' "$output_file"; then
    echo pass
    return
  fi
  if grep -q 'unknown flag\|flag provided but not defined\|error loading config file' "$output_file"; then
    echo harness-error
    return
  fi
  echo inconclusive
}

start_tls13_server() {
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "$WORKDIR/tls.key" -out "$WORKDIR/tls.crt" \
    -days 1 -subj "/CN=127.0.0.1" >/dev/null 2>&1 \
    || die "could not generate a self-signed certificate for the test server"
  openssl s_server -accept "127.0.0.1:${TLS_PREFLIGHT_PORT}" -tls1_3 \
    -cert "$WORKDIR/tls.crt" -key "$WORKDIR/tls.key" -www -quiet \
    >/dev/null 2>&1 &
  S_SERVER_PID=$!
}

# Sanity-check the listener from the runner before blaming kubectl: with
# -brief, OpenSSL reports the negotiated protocol as soon as the handshake
# completes. The full output is captured first and only checked after
# OpenSSL finishes, so however the connection ends (stdin is closed
# immediately), the handshake report decides.
probe_tls13_server() {
  local attempt probe
  for attempt in 1 2 3 4 5; do
    probe=$(timeout 10 openssl s_client -connect "127.0.0.1:${TLS_PREFLIGHT_PORT}" -tls1_3 -brief </dev/null 2>&1 || true)
    if printf '%s\n' "$probe" | grep -q 'Protocol version: *TLSv1.3'; then
      echo "TLS 1.3-only test server is up on 127.0.0.1:${TLS_PREFLIGHT_PORT}."
      return 0
    fi
    sleep 2
  done
  die "the local TLS 1.3 test server did not come up; the preflight cannot run"
}

check_component() {
  local component="$1" image="$2"
  local output_file="${WORKDIR}/${component}.output"
  echo "=== ${component}: checking kubectl from ${image} (native platform)"
  local attempt rc verdict
  for attempt in $(seq 1 "$TLS_PREFLIGHT_ATTEMPTS"); do
    echo "--- attempt ${attempt}/${TLS_PREFLIGHT_ATTEMPTS}"
    # Output goes to a file: Go panic traces contain null bytes that bash
    # command substitution would strip (with a warning), and the file keeps
    # container-runtime errors strictly separate from kubectl's own output.
    set +e
    timeout "$TLS_PREFLIGHT_TIMEOUT" "$CONTAINER_RUNTIME" run --rm --network host \
      --entrypoint kubectl "$image" \
      --server="https://127.0.0.1:${TLS_PREFLIGHT_PORT}" \
      --insecure-skip-tls-verify \
      --token=tls-preflight \
      get --raw / >"$output_file" 2>&1
    rc=$?
    set -e
    cat "$output_file"
    verdict=$(classify_result "$rc" "$output_file")
    case "$verdict" in
      pass)
        echo "=== ${component}: PASSED, kubectl in ${image} completed the TLS 1.3 handshake."
        return 0
        ;;
      panic)
        echo "ERROR: kubectl in ${image} panicked during the TLS 1.3 handshake." >&2
        echo "This is the AROSLSRE-2021 / AROSLSRE-2481 regression class: the image must not be" >&2
        echo "merged until it is rebuilt with a kubectl/Go-OpenSSL integration that passes this" >&2
        echo "same TLS 1.3 check (see AROSLSRE-2476)." >&2
        return 1
        ;;
      runtime-error)
        echo "--- container-runtime failure (image pull/run), not a kubectl result; retrying in 10s ..."
        sleep 10
        ;;
      harness-error)
        echo "ERROR: kubectl in ${image} rejected the check invocation itself (unknown flag or config error); the preflight cannot verify this image." >&2
        return 1
        ;;
      inconclusive)
        echo "--- inconclusive (connection-level) result; retrying in 10s ..."
        sleep 10
        ;;
    esac
  done
  echo "ERROR: could not verify ${image} after ${TLS_PREFLIGHT_ATTEMPTS} attempts; failing closed so an unverified digest cannot merge." >&2
  return 1
}

main() {
  if command -v podman >/dev/null 2>&1; then
    CONTAINER_RUNTIME=podman
  elif command -v docker >/dev/null 2>&1; then
    CONTAINER_RUNTIME=docker
  else
    die "neither podman nor docker is available"
  fi
  command -v openssl >/dev/null 2>&1 || die "openssl is not available"
  command -v timeout >/dev/null 2>&1 || die "timeout is not available"
  cd "$ROOT"
  [[ -f "$TLS_PREFLIGHT_CONFIG" ]] || die "config file ${TLS_PREFLIGHT_CONFIG} not found"

  read -ra components <<<"$TLS_PREFLIGHT_COMPONENTS"

  # Presubmit diff gate: only check components whose digest changed relative
  # to the Prow base commit. Periodics, rehearsals and local runs have no
  # usable base and check everything.
  local base_available=false
  if [[ -n "${PULL_BASE_SHA:-}" ]] && git cat-file -e "${PULL_BASE_SHA}^{commit}" 2>/dev/null; then
    base_available=true
  elif [[ -n "${PULL_BASE_SHA:-}" ]]; then
    echo "PULL_BASE_SHA=${PULL_BASE_SHA} not found in the local clone; checking all components."
  fi

  local pending=() images=() component head_digest base_digest image
  for component in "${components[@]}"; do
    head_digest=$(extract_image_field "$component" digest "$TLS_PREFLIGHT_CONFIG") \
      || die "component ${component} is listed for the TLS preflight but has no defaults.${component}.image.digest in ${TLS_PREFLIGHT_CONFIG}"
    if [[ "$base_available" == true ]]; then
      base_digest=$(git show "${PULL_BASE_SHA}:${TLS_PREFLIGHT_CONFIG}" 2>/dev/null \
        | extract_image_field "$component" digest - || true)
      if [[ -n "$base_digest" && "$base_digest" == "$head_digest" ]]; then
        echo "=== ${component}: digest unchanged (${head_digest}); nothing to check."
        continue
      fi
      echo "=== ${component}: digest changed: ${base_digest:-<absent>} -> ${head_digest}"
    fi
    image=$(image_ref "$component") || die "cannot resolve the image for component ${component}"
    pending+=("$component")
    images+=("$image")
  done

  if [[ ${#pending[@]} -eq 0 ]]; then
    echo "TLS preflight: no component digests changed; nothing to check."
    return 0
  fi

  WORKDIR=$(mktemp -d /tmp/tls-preflight.XXXXXX)
  S_SERVER_PID=""
  cleanup() {
    if [[ -n "$S_SERVER_PID" ]]; then
      kill "$S_SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$WORKDIR"
  }
  trap cleanup EXIT

  start_tls13_server
  probe_tls13_server

  local failures=0 i
  for i in "${!pending[@]}"; do
    if ! check_component "${pending[$i]}" "${images[$i]}"; then
      failures=$((failures + 1))
    fi
  done

  if [[ "$failures" -ne 0 ]]; then
    echo "TLS preflight: FAILED (${failures} of ${#pending[@]} checked components)." >&2
    return 1
  fi
  echo "TLS preflight: PASSED (${#pending[@]} component(s) verified)."
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  set -euo pipefail
  main "$@"
fi
