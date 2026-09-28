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

# Generates dev-infrastructure/dev-ci/e2e-subscription-providers/pipeline.yaml
# from the E2E subscription inventory in config/config-dev-ci.yaml, so the
# provider-registration targets always follow ci.<env>.e2eSubscriptions rather
# than being hand-maintained alongside it.
#
# Why generate instead of templating the pipeline directly: templatize parses
# pipeline.yaml as raw YAML *before* Go-template rendering (collectBicepparamFiles
# in tooling/templatize/cmd/pipeline/validate/bicepparam.go), so a `{{ range }}`
# emitting resourceGroups entries makes the file unparseable. Generating keeps
# the committed file plain YAML while keeping the inventory the single source.
#
# Writes to $1 if given, otherwise in place. Run via:
#   make generate-e2e-provider-pipeline

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

YQ="${YQ:-yq}"
CONFIG_FILE="$REPO_ROOT/config/config-dev-ci.yaml"
OUTPUT_FILE="${1:-$REPO_ROOT/dev-infrastructure/dev-ci/e2e-subscription-providers/pipeline.yaml}"

BANNER_WIDTH=78

# banner "DEV" -> "# ── DEV ────────...", padded to BANNER_WIDTH characters.
banner() {
  local label="$1"
  local prefix="# ── ${label} "
  local fill=$((BANNER_WIDTH - ${#prefix}))
  printf '%s' "$prefix"
  for ((i = 0; i < fill; i++)); do printf '─'; done
  printf '\n'
}

{
  cat <<'EOF'
$schema: "pipeline.schema.v1"
serviceGroup: Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionProviders
rolloutName: Dev CI E2E Subscription Provider Registration Rollout
# Code generated from config/config-dev-ci.yaml. DO NOT EDIT.
# Regenerate with: make generate-e2e-provider-pipeline
#
# PRIVILEGED pipeline: registers the required Azure resource providers on every
# ARO-HCP-managed E2E subscription. The provider list lives in
# config/config-dev-ci.yaml under ci.e2eSubscriptionProviders (the single
# source of truth); add new provider dependencies there, not here.
#
# The targets below are derived from the ci.<env>.e2eSubscriptions inventories
# in that same file, so onboarding a subscription there and regenerating is all
# that is required. Each resource group entry targets one E2E subscription; the
# resource group name is a placeholder, because ProviderFeatureRegistration
# steps need only the subscription, not an existing resource group. Subscriptions
# are resolved from their display names via the Azure API using the invoking
# OWNERS member's CLI credentials (AZURE_TOKEN_CREDENTIALS=dev). Run with:
#   make dev-ci-privileged-local-run
resourceGroups:
EOF

  # Iterate ci.<env> sections in document order so a newly added environment is
  # picked up without editing this script.
  envs="$("$YQ" -r '.clouds.dev.defaults.ci | to_entries | .[]
                      | select(.value.e2eSubscriptions) | .key' "$CONFIG_FILE")"

  if [[ -z "$envs" ]]; then
    echo "ERROR: no ci.<env>.e2eSubscriptions inventories found in $CONFIG_FILE" >&2
    exit 1
  fi

  for env in $envs; do
    banner "$(echo "$env" | tr '[:lower:]' '[:upper:]')"
    index=0
    while IFS= read -r subscription; do
      [[ -z "$subscription" ]] && continue
      # Emitted as a single-quoted YAML scalar; an apostrophe would need
      # doubling, so reject it rather than silently producing broken YAML.
      if [[ "$subscription" == *"'"* ]]; then
        echo "ERROR: subscription name contains a single quote, which this generator cannot emit safely: $subscription" >&2
        exit 1
      fi
      cat <<EOF
- name: e2e-providers-${env}-${index}
  resourceGroup: provider-registration
  subscription: '${subscription}'
  steps:
  - name: register-providers
    action: ProviderFeatureRegistration
    providerConfigRef: ci.e2eSubscriptionProviders
EOF
      index=$((index + 1))
    done < <("$YQ" -r ".clouds.dev.defaults.ci.${env}.e2eSubscriptions[].name" "$CONFIG_FILE")
  done
} >"$OUTPUT_FILE"

echo "wrote ${OUTPUT_FILE#"$REPO_ROOT"/}"
