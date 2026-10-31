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

# Checks the latest stable GitHub release for release-tracked tools pinned in
# versions.mk and rewrites each pin and its owning Dockerfile ARG.
# Add each release-tracked tool's repository and Dockerfile to the maps below.
# ENVTEST_VERSION is a manual compatibility pin; see versions.mk before changing it.
# Pass *_VERSION variable names to update only those tools; omit them to check all.
# --metadata-json reports the upstream mapping without fetching or changing pins.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSIONS_MK="$REPO_ROOT/versions.mk"

# Maps each versions.mk variable to the GitHub repo whose releases it tracks.
declare -A TOOL_REPOS=(
  [PROMTOOL_VERSION]="prometheus/prometheus"
  [BICEP_VERSION]="Azure/bicep"
  [GOTESTSUM_VERSION]="gotestyourself/gotestsum"
)

# Each release-tracked pin has one source-free Dockerfile default to synchronize.
declare -A TOOL_DOCKERFILES=(
  [PROMTOOL_VERSION]="Dockerfile"
  [BICEP_VERSION]="Dockerfile"
  [GOTESTSUM_VERSION]="Dockerfile.nested-podman"
)

if [[ "${1:-}" == "--metadata-json" ]]; then
  for var in "${!TOOL_REPOS[@]}"; do
    jq -cn --arg variable "$var" --arg repository "${TOOL_REPOS[$var]}" \
      '{variable: $variable, repository: $repository}'
  done | jq -s 'sort_by(.variable)'
  exit 0
fi

changed=0

tools=("$@")
if [[ "${#tools[@]}" -eq 0 ]]; then
  tools=("${!TOOL_REPOS[@]}")
fi

for var in "${tools[@]}"; do
  repo="${TOOL_REPOS[$var]:-}"
  if [[ -z "$repo" ]]; then
    echo "ERROR: unsupported tool version variable: ${var}" >&2
    exit 1
  fi
  dockerfile="$REPO_ROOT/${TOOL_DOCKERFILES[$var]}"

  current="$(grep -oP "^${var}\s*\?=\s*\K\S*" "$VERSIONS_MK" || true)"
  if [[ -z "$current" ]]; then
    echo "ERROR: could not find ${var} in $VERSIONS_MK" >&2
    exit 1
  fi

  curl_auth_args=()
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    curl_auth_args=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  fi
  latest_tag="$(curl -fsSL --show-error --retry 3 "${curl_auth_args[@]}" "https://api.github.com/repos/${repo}/releases/latest" | jq -r '.tag_name')"
  latest="${latest_tag#v}"

  if [[ -z "$latest" || "$latest" == "null" ]]; then
    echo "ERROR: could not determine latest release for ${repo}" >&2
    exit 1
  fi

  if [[ "$latest" == "$current" ]]; then
    echo "  ${var}: ${current} is already latest"
    continue
  fi

  # Only bump forward: if the pinned version is already ahead of the latest
  # GitHub release (e.g. a pre-release or hotfix tag), leave it untouched
  # instead of downgrading it.
  highest="$(printf '%s\n%s\n' "$current" "$latest" | sort -V | tail -1)"
  if [[ "$highest" == "$current" ]]; then
    echo "  ${var}: ${current} is already ahead of latest release ${latest}, leaving as-is"
    continue
  fi

  echo "  ${var}: ${current} -> ${latest}"
  sed -i -E "s/^(${var}[[:space:]]*\?=[[:space:]]*).*$/\1${latest}/" "$VERSIONS_MK"
  sed -i -E "s/^(ARG ${var}=).*$/\1${latest}/" "$dockerfile"

  if ! grep -qE "^${var}[[:space:]]*\?=[[:space:]]*${latest}$" "$VERSIONS_MK"; then
    echo "ERROR: failed to update ${var} in $VERSIONS_MK" >&2
    exit 1
  fi
  if ! grep -qE "^ARG ${var}=${latest}$" "$dockerfile"; then
    echo "ERROR: failed to update ARG ${var} in $dockerfile" >&2
    exit 1
  fi

  changed=1
done

if [[ "$changed" -eq 0 ]]; then
  echo "All selected tool versions are already up to date."
fi
