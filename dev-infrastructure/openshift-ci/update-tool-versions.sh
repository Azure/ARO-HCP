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

# Checks the latest stable GitHub release for every tool pinned in
# versions.mk and rewrites the corresponding *_VERSION variable in place
# when a newer version is available. Adding a new pinned tool to
# versions.mk only requires adding its GitHub repo to TOOL_REPOS below;
# this script never needs to change for the bicep/promtool case
# specifically.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSIONS_MK="$REPO_ROOT/versions.mk"

# Maps each versions.mk variable to the GitHub repo whose releases it tracks.
declare -A TOOL_REPOS=(
  [PROMTOOL_VERSION]="prometheus/prometheus"
  [BICEP_VERSION]="Azure/bicep"
)

changed=0

for var in "${!TOOL_REPOS[@]}"; do
  repo="${TOOL_REPOS[$var]}"

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
  sed -i -E "s/^(ARG ${var}=).*$/\1${latest}/" "$REPO_ROOT/Dockerfile"

  if ! grep -qE "^${var}[[:space:]]*\?=[[:space:]]*${latest}$" "$VERSIONS_MK"; then
    echo "ERROR: failed to update ${var} in $VERSIONS_MK" >&2
    exit 1
  fi
  if ! grep -qE "^ARG ${var}=${latest}$" "$REPO_ROOT/Dockerfile"; then
    echo "ERROR: failed to update ARG ${var} in $REPO_ROOT/Dockerfile" >&2
    exit 1
  fi

  changed=1
done

if [[ "$changed" -eq 0 ]]; then
  echo "All pinned tool versions are already up to date."
fi
